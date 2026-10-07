package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// seedWasteWorkflow writes one workflow at fixed instants that trips the
// waste detector's defaults: five consecutive steps from one agent (a
// loop) and one step past the oversized-context ceiling.
func seedWasteWorkflow(t *testing.T) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "trace.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	for i, in := range []int64{1_200, 4_800, 40_000, 9_000, 12_500} {
		env := &eventschema.Envelope{
			ID: fmt.Sprintf("wt-%d", i), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(time.Duration(i) * time.Minute),
			Source: "test",
			Payload: &eventschema.PromptEvent{
				PromptHash: fmt.Sprintf("sha256:%d", i), Provider: eventschema.ProviderAnthropic,
				RequestModel: "claude-sonnet-4-5", ResponseModel: "claude-sonnet-4-5",
				InputTokens: in, OutputTokens: 300, TotalTokens: in + 300, ContextSize: in,
				Status: 200, WorkflowID: "wf-waste", AgentID: "agent-loop", SessionID: "sess-waste",
			},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// TestWorkflowTraceCharacterization pins tokenops_workflow_trace and
// tokenops_review_work so moving the trace onto the workflowtrace
// capability cannot change what an agent reads.
func TestWorkflowTraceCharacterization(t *testing.T) {
	store := seedWasteWorkflow(t)
	eng := spend.NewEngine(spend.DefaultTable())
	newSrv := func(d Deps) *Server {
		srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
		if err := RegisterTools(srv, d); err != nil {
			t.Fatal(err)
		}
		return srv
	}
	base := Deps{Store: store, Aggregator: analytics.New(store, eng), Spend: eng}
	live := base
	live.WasteConfig = func() waste.Config { return waste.Config{ContextGrowthLimitTokens: 1} }

	cases := []struct {
		golden string
		srv    *Server
		tool   string
		args   any
	}{
		{"workflowtrace/trace.json", newSrv(base), "tokenops_workflow_trace", map[string]any{"workflow_id": "wf-waste"}},
		{"workflowtrace/trace_live_config.json", newSrv(live), "tokenops_workflow_trace", map[string]any{"workflow_id": "wf-waste"}},
		{"workflowtrace/review.json", newSrv(base), "tokenops_review_work", map[string]any{"workflow_id": "wf-waste"}},
		{"workflowtrace/review_unknown.json", newSrv(base), "tokenops_review_work", map[string]any{"workflow_id": "wf-never-seen"}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			assertGolden(t, tc.golden, roundFloats(execTool(t, tc.srv, tc.tool, tc.args)))
		})
	}
}
