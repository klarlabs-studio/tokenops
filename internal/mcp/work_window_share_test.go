package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// prepare_work estimates the task's share of the plan window and records
// where the window stood; review_work measures what the task took and sets
// it against the estimate.
func TestPrepareAndReviewWorkCarryTheWindowShare(t *testing.T) {
	d := tightWindowDeps(t) // anthropic on claude-max-20x, meter at 95%
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterRoutingAdviceTools(srv, d); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_prepare_work", map[string]any{
		"instruction": "fix the flaky timing test", "provider": "anthropic",
		"model": "claude-opus-5", "repository": "tokenops",
	})
	var prep prepareWorkResult
	if err := json.Unmarshal([]byte(out), &prep); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	ws := prep.WindowShare
	if ws == nil {
		t.Fatalf("prepare_work has no window_share:\n%s", out)
	}
	if ws.TaskClass != "edit" || ws.Scope != "tokenops" || ws.Window != "5h" || !ws.WindowKnown || ws.LeftPct != 5 {
		t.Errorf("window_share = %+v, want edit in tokenops, 5h window, 5%% left", ws)
	}
	if ws.Estimate.Estimated() || !strings.Contains(ws.Summary, "no estimate yet: 0 of 5") {
		t.Errorf("window_share offered an estimate with no history: %+v", ws)
	}
	if !ws.Recorded {
		t.Error("prepare did not record the window's state")
	}
	if strings.Contains(out, "flaky timing") {
		t.Error("the instruction was echoed into the result")
	}

	// The window moves while the task runs.
	reading := &eventschema.Envelope{
		ID: "meter-later", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: time.Now().UTC(),
		Source:     "claude-usage-meter",
		Attributes: map[string]string{"five_hour_used_pct": "98"},
		Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, Status: 200},
	}
	if err := d.Store.Append(context.Background(), reading); err != nil {
		t.Fatal(err)
	}
	eng := spend.NewEngine(spend.DefaultTable())
	review := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterTools(review, Deps{Store: d.Store, Aggregator: analytics.New(d.Store, eng), Spend: eng}); err != nil {
		t.Fatal(err)
	}
	got := execTool(t, review, "tokenops_review_work", map[string]any{"workflow_id": prep.WorkflowID})
	for _, want := range []string{`"window_share"`, `"measured": true`, `"share_pct": 3`, `took 3% of the 5h window`, `Plan window: took 3%`} {
		if !strings.Contains(got, want) {
			t.Errorf("review missing %s:\n%s", want, got)
		}
	}
}
