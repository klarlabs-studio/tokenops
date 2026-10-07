package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.klarlabs.de/mcp/server"

	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// fullServer registers every tool group as serve does.
func fullServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "c.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := NewServer("tokenops", "test", nil)
	eng := spend.NewEngine(spend.DefaultTable())
	for _, err := range []error{
		RegisterTools(srv, Deps{Store: store, Spend: eng, Aggregator: analytics.New(store, eng)}),
		RegisterRulesTools(srv),
		RegisterParityTools(srv, ParityDeps{Store: store, Spend: eng}),
		RegisterControlTools(srv, ControlDeps{}),
		RegisterPlanTools(srv, PlanDeps{}),
		RegisterFindingsTool(srv, PlanDeps{}),
		RegisterAgentDXTools(srv, AgentDXDeps{}),
		RegisterExplainTools(srv),
		RegisterStoryTools(srv, StoryDeps{}),
		RegisterVerifyTool(srv, VerifyDeps{}),
		RegisterOutcomeTools(srv, OutcomeDeps{Store: store}),
		RegisterDecisionTools(srv, DecisionDeps{Store: store}),
		RegisterExperimentTools(srv, ExperimentDeps{Manager: experiments.New(store)}),
		RegisterRoutingAdviceTools(srv, RoutingAdviceDeps{}),
		RegisterApprovalTools(srv, ApprovalDeps{}),
		RegisterModeTools(srv, ModeDeps{}),
		RegisterCoachTool(srv, ModeDeps{}),
		RegisterDataSourcesTool(srv, DataSourcesDeps{Store: store}),
		RegisterFmtTools(srv, FmtDeps{}),
		RegisterCoachTools(srv, CoachDeps{}),
		RegisterGapTools(srv, GapDeps{}),
		RegisterSetupTools(srv, SetupDeps{}),
	} {
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	return srv
}

// Every view routes to a tool serve registers: a route to a tool that is
// not there would be dropped without a word.
func TestEveryViewReachesARegisteredTool(t *testing.T) {
	srv := fullServer(t)
	registered := map[string]bool{}
	for _, ti := range srv.Tools() {
		registered[ti.Name] = true
	}
	reached := map[string]bool{}
	for public, inner := range PublicRoutes() {
		for _, name := range inner {
			if !registered[name] {
				t.Errorf("%s routes to %s, which is not registered", public, name)
			}
			reached[name] = true
		}
	}
	for name := range registered {
		if !reached[name] {
			t.Errorf("%s is registered but no public tool reaches it", name)
		}
	}
}

var wantPublic = []string{
	"tokenops_coach", "tokenops_configure", "tokenops_experiment", "tokenops_explain", "tokenops_fmt",
	"tokenops_glance", "tokenops_outcome", "tokenops_prepare_work", "tokenops_pricing", "tokenops_records",
	"tokenops_review_work", "tokenops_routing", "tokenops_rules", "tokenops_sessions", "tokenops_spend",
	"tokenops_status",
}

// Agents see sixteen tools, each titled, annotated by what it may do,
// with an output schema, and every parameter described.
func TestConsolidatedToolsAreDescribed(t *testing.T) {
	srv := fullServer(t)
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]kind{}
	for _, p := range publicTools() {
		kinds[p.name] = p.kind
	}
	names := make([]string, 0, len(srv.Tools()))
	for _, ti := range srv.Tools() {
		names = append(names, ti.Name)
		tool, _ := srv.GetTool(ti.Name)
		b, err := json.Marshal(ti)
		if err != nil {
			t.Fatal(err)
		}
		var def struct {
			Title       string `json:"title"`
			Annotations struct {
				Title       string `json:"title"`
				ReadOnly    *bool  `json:"readOnlyHint"`
				Destructive *bool  `json:"destructiveHint"`
			} `json:"annotations"`
			InputSchema struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"inputSchema"`
		}
		if err := json.Unmarshal(b, &def); err != nil {
			t.Fatal(err)
		}
		if def.Annotations.Title == "" && def.Title == "" {
			t.Errorf("%s has no title", ti.Name)
		}
		readOnly := def.Annotations.ReadOnly != nil && *def.Annotations.ReadOnly
		if readOnly != (kinds[ti.Name] == reads) {
			t.Errorf("%s readOnlyHint = %v", ti.Name, readOnly)
		}
		if kinds[ti.Name] == changes && (def.Annotations.Destructive == nil || !*def.Annotations.Destructive) {
			t.Errorf("%s changes settings but is not marked destructive", ti.Name)
		}
		if tool.OutputSchema() == nil {
			t.Errorf("%s has no output schema", ti.Name)
		}
		for param, p := range def.InputSchema.Properties {
			if p.Description == "" {
				t.Errorf("%s.%s has no description", ti.Name, param)
			}
		}
	}
	sort.Strings(names)
	if strings.Join(names, " ") != strings.Join(wantPublic, " ") {
		t.Errorf("public tools:\n%v\nwant:\n%v", names, wantPublic)
	}
}

func callPublic(t *testing.T, srv *Server, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	tool, ok := srv.GetTool(name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	b, _ := json.Marshal(args)
	res, err := tool.Execute(context.Background(), b)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	raw, _ := json.Marshal(res)
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// A view's answer comes back under its name; a parameter it does not take
// and a view that does not exist are the caller's to correct.
func TestConsolidatedRouting(t *testing.T) {
	srv := fullServer(t)
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	out, err := callPublic(t, srv, "tokenops_spend", map[string]any{"view": "burn", "hours": 6})
	if err != nil || out["view"] != "burn" || out["burn"] == nil {
		t.Fatalf("spend burn = %v %v", out, err)
	}
	if out, err := callPublic(t, srv, "tokenops_status", map[string]any{"view": "version"}); err != nil || out["version"] == nil {
		t.Errorf("status version = %v %v", out, err)
	}
	var ie *server.ToolInputError
	_, err = callPublic(t, srv, "tokenops_spend", map[string]any{"view": "burn", "since": "1h"})
	if !errors.As(err, &ie) || !strings.Contains(ie.Message, "since does not apply to view burn") {
		t.Errorf("stray parameter: %v", err)
	}
	if _, err := callPublic(t, srv, "tokenops_spend", map[string]any{"view": "nope"}); !errors.As(err, &ie) {
		t.Errorf("unknown view: %v", err)
	}
	// explain picks the decision route by its ID.
	if out, err := callPublic(t, srv, "tokenops_explain", map[string]any{"term": "wall-clock"}); err != nil || out["view"] != "term" {
		t.Errorf("explain term = %v %v", out, err)
	}
	out, err = callPublic(t, srv, "tokenops_explain", map[string]any{"decision_id": "decision:missing"})
	if d, _ := out["decision"].(map[string]any); err != nil || out["view"] != "decision" || d["error"] != "decision_not_found" {
		t.Errorf("an unknown decision = %v %v", out, err)
	}
	// configure reaches the mode tool, whose own answer (no config yet,
	// here) comes back unchanged; an invalid mode never reaches it.
	if _, err := callPublic(t, srv, "tokenops_configure", map[string]any{"setting": "mode"}); err == nil || !strings.Contains(err.Error(), "tokenops init") {
		t.Errorf("configure mode without a config: %v", err)
	}
	if _, err := callPublic(t, srv, "tokenops_configure", map[string]any{"setting": "mode", "mode": "frantic"}); !errors.As(err, &ie) || !strings.Contains(ie.Message, "must be one of") {
		t.Errorf("an invalid mode: %v", err)
	}
}
