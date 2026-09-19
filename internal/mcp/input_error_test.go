package mcp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/mcp/server"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Every refusal the tools were written to explain reached the agent as
// "internal error": the MCP library masks any handler error that is not a
// ToolInputError, so "a budget needs a ceiling" arrived as nothing the agent
// could act on. Found by driving a release candidate over stdio. Each case
// here is a mistake an agent can make; the error must arrive as a tool
// input error carrying the explanation.
func TestCallerMistakesReachTheAgent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(dir, "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	eng := spend.NewEngine(spend.DefaultTable())

	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, reg := range []error{
		RegisterModeTools(srv, ModeDeps{ConfigPath: cfgPath}),
		RegisterSetupTools(srv, SetupDeps{ConfigPath: cfgPath}),
		RegisterApprovalTools(srv, ApprovalDeps{ConfigPath: cfgPath, StorePath: filepath.Join(dir, "approvals.jsonl")}),
		RegisterTools(srv, Deps{Store: store, Spend: eng, Aggregator: analytics.New(store, eng)}),
		RegisterParityTools(srv, ParityDeps{Store: store, Spend: eng}),
		RegisterRulesTools(srv),
		RegisterDataSourcesTool(srv, DataSourcesDeps{Store: store}),
	} {
		if reg != nil {
			t.Fatal(reg)
		}
	}

	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"tokenops_budget_set", map[string]any{"name": "b", "window": "daily"}, "ceiling"},
		{"tokenops_plan_set", map[string]any{"provider": "anthropic", "plan": "claude-enterprise"}, "spend limit"},
		{"tokenops_plan_set", map[string]any{"provider": "anthropic", "plan": "no-such-plan"}, "no-such-plan"},
		{"tokenops_routing_rule_set", map[string]any{"provider": "anthropic", "from_model": "m", "delete": true}, "no routing rule"},
		{"tokenops_routing_decide", map[string]any{"key": "missing", "decision": "approve"}, "no routing proposal"},
		{"tokenops_spend_summary", map[string]any{"since": "yesterday-ish"}, "since"},
		{"tokenops_top_consumers", map[string]any{"until": "not-a-time"}, "cannot parse"},
		{"tokenops_workflow_trace", map[string]any{"workflow_id": ""}, "workflow_id is required"},
		{"tokenops_replay", map[string]any{}, "provide session_id"},
		{"tokenops_data_sources", map[string]any{"since": "whenever"}, ""},
		{"tokenops_rules_analyze", map[string]any{"root": filepath.Join(dir, "absent")}, ""},
	} {
		err := execToolErr(t, srv, tc.tool, tc.args)
		if err == nil {
			t.Errorf("%s %v: no error", tc.tool, tc.args)
			continue
		}
		var ie *server.ToolInputError
		if !errors.As(err, &ie) {
			t.Errorf("%s %v: %T %q would reach the agent as \"internal error\"", tc.tool, tc.args, err, err)
			continue
		}
		if tc.want != "" && !strings.Contains(ie.Message, tc.want) {
			t.Errorf("%s: message %q does not explain (%q)", tc.tool, ie.Message, tc.want)
		}
	}
}

func TestInputErrorIsIdempotent(t *testing.T) {
	first := inputError(errors.New("bad"))
	if again := inputError(first); again != first {
		t.Error("re-wrapping changed the error")
	}
	if inputError(nil) != nil {
		t.Error("nil became an error")
	}
}
