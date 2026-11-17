package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/workquote"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/work/quote"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// writeQuoteBasis stores n synthetic edits on model costing 1..n dollars,
// with the five-hour window at 2 dollars a percent.
func writeQuoteBasis(t *testing.T, model string, n int) string {
	t.Helper()
	now := time.Now().UTC()
	b := workquote.Basis{Version: workquote.Version, ComputedAt: now.Add(-time.Hour),
		Rates: []plans.ValueRate{{Provider: "anthropic", Window: "5-hour", Duration: 5 * time.Hour, Windows: 4, USDPerPct: 2}}}
	for i := range n {
		b.Samples = append(b.Samples, quote.Sample{Client: "claude-code", Provider: "anthropic", Model: model, Kind: "edit",
			At: now.Add(-time.Duration(i+1) * time.Hour), USD: float64(i + 1), Tokens: int64(i+1) * 1000})
	}
	dir := t.TempDir()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work-quote-basis.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// prepare_work quotes the task from the stored history and records the
// quote; review_work checks it against the share of the window it took.
func TestPrepareAndReviewWorkCarryTheQuote(t *testing.T) {
	d := tightWindowDeps(t) // anthropic on claude-max, meter at 95%
	d.QuoteDir = writeQuoteBasis(t, "claude-opus-5", 20)
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterRoutingAdviceTools(srv, d); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_prepare_work", map[string]any{
		"instruction": "fix the flaky timing test", "provider": "anthropic", "model": "claude-opus-5",
	})
	var prep prepareWorkResult
	if err := json.Unmarshal([]byte(out), &prep); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	q := prep.Quote
	if q == nil || !q.Quoted() || !q.Estimate || q.P50USD != 10.5 || q.P90USD != 18.1 || q.Basis != quote.BasisModelKind || !q.Recorded {
		t.Fatalf("quote = %+v\n%s", q, out)
	}
	if strings.Contains(out, "flaky timing") {
		t.Error("the instruction was echoed into the result")
	}

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
	for _, want := range []string{`"quote"`, `estimate check: took 3.0% of the 5h window against a p90 of 9.1% (within)`, `Quote: estimate check`} {
		if !strings.Contains(got, want) {
			t.Errorf("review missing %s:\n%s", want, got)
		}
	}
}

// Without a stored history the quote says so and gives no number.
func TestPrepareWorkWithoutAQuoteBasis(t *testing.T) {
	d := tightWindowDeps(t)
	d.QuoteDir = t.TempDir()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterRoutingAdviceTools(srv, d); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_prepare_work", map[string]any{
		"instruction": "fix it", "provider": "anthropic", "model": "claude-opus-5",
	})
	var prep prepareWorkResult
	if err := json.Unmarshal([]byte(out), &prep); err != nil {
		t.Fatal(err)
	}
	if prep.Quote == nil || prep.Quote.Status != workquote.StatusNoBasis || prep.Quote.P90USD != 0 {
		t.Fatalf("quote = %+v", prep.Quote)
	}
}
