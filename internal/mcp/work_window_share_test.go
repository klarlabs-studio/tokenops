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
	d := tightWindowDeps(t) // anthropic on claude-max, meter at 95%
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

// The spread is added beside the window_share fields agents already read;
// none of them is renamed or dropped.
func TestPrepareWorkKeepsItsJSONAndAddsTheSpread(t *testing.T) {
	d := tightWindowDeps(t)
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterRoutingAdviceTools(srv, d); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_prepare_work", map[string]any{
		"instruction": "fix the flaky timing test", "provider": "anthropic",
		"model": "claude-opus-5", "repository": "tokenops",
	})
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	for _, k := range []string{"workflow_id", "attribution_header", "review", "recommendation", "plan_headroom", "window_share"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("prepare_work lost %q", k)
		}
	}
	ws, _ := raw["window_share"].(map[string]any)
	for _, k := range []string{"status", "task_class", "window", "window_known", "used_pct", "left_pct", "estimate", "suggestion", "summary", "recorded", "spread"} {
		if _, ok := ws[k]; !ok {
			t.Errorf("window_share lacks %q:\n%s", k, out)
		}
	}
	est, _ := ws["estimate"].(map[string]any)
	for _, k := range []string{"status", "samples", "min_samples"} {
		if _, ok := est[k]; !ok {
			t.Errorf("window_share.estimate lost %q", k)
		}
	}
	sp, _ := ws["spread"].(map[string]any)
	if sp["status"] != "too_few_samples" || sp["samples"] != float64(0) || sp["min_samples"] != float64(5) || sp["fallback"] != "none" {
		t.Errorf("spread = %v, want too_few_samples, 0 of 5, no fallback", sp)
	}
}
