package analytics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mkPrompt(id string, ts time.Time, model string, in, out int64, cost float64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: ts, Source: "test",
		Payload: &eventschema.PromptEvent{
			PromptHash: "h-" + id, Provider: eventschema.ProviderOpenAI,
			RequestModel: model, InputTokens: in, OutputTokens: out, TotalTokens: in + out,
			CostUSD: cost,
		},
	}
}

func TestAggregateByHourBucketsAndSums(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("a", base.Add(5*time.Minute), "gpt-4o", 100, 50, 0.001),
		mkPrompt("b", base.Add(20*time.Minute), "gpt-4o", 200, 100, 0.002),
		mkPrompt("c", base.Add(70*time.Minute), "gpt-4o", 300, 150, 0.003), // next bucket
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	agg := New(store, nil)
	rows, err := agg.AggregateBy(ctx, Filter{}, BucketHour, GroupNone)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d buckets, want 2: %+v", len(rows), rows)
	}
	if rows[0].Requests != 2 || rows[0].InputTokens != 300 || rows[0].OutputTokens != 150 {
		t.Errorf("bucket 0 unexpected: %+v", rows[0])
	}
	if rows[1].Requests != 1 || rows[1].InputTokens != 300 {
		t.Errorf("bucket 1 unexpected: %+v", rows[1])
	}
	if rows[1].BucketStart.Sub(rows[0].BucketStart) != time.Hour {
		t.Errorf("buckets not 1h apart: %s vs %s", rows[0].BucketStart, rows[1].BucketStart)
	}
}

func TestAggregateByModel(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("a", base, "gpt-4o", 100, 50, 0.001),
		mkPrompt("b", base.Add(time.Minute), "gpt-4o-mini", 200, 100, 0.0005),
		mkPrompt("c", base.Add(2*time.Minute), "gpt-4o", 300, 100, 0.0015),
	}
	_ = store.AppendBatch(ctx, envs)

	agg := New(store, nil)
	rows, err := agg.AggregateBy(ctx, Filter{}, BucketHour, GroupModel)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	got := map[string]Row{}
	for _, r := range rows {
		got[r.GroupKey] = r
	}
	if got["gpt-4o"].Requests != 2 || got["gpt-4o"].InputTokens != 400 {
		t.Errorf("gpt-4o group: %+v", got["gpt-4o"])
	}
	if got["gpt-4o-mini"].Requests != 1 {
		t.Errorf("gpt-4o-mini group: %+v", got["gpt-4o-mini"])
	}
}

func TestAggregateRecomputesMissingCost(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	// Cost left at 0 — aggregator should recompute via spend.Engine.
	env := mkPrompt("x", base, "gpt-4o-mini", 1_000_000, 1_000_000, 0)
	_ = store.Append(ctx, env)

	eng := spend.NewEngine(spend.DefaultTable())
	agg := New(store, eng)
	rows, err := agg.AggregateBy(ctx, Filter{}, BucketHour, GroupNone)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if r.CostUSD <= 0 {
		t.Errorf("cost not recomputed: %+v", r)
	}
	if r.CostRecomputed != 1 {
		t.Errorf("CostRecomputed = %d, want 1", r.CostRecomputed)
	}
	// gpt-4o-mini at 1M+1M tokens ≈ $0.15 + $0.60 = $0.75
	if r.CostUSD < 0.7 || r.CostUSD > 0.8 {
		t.Errorf("cost out of expected band: %.4f", r.CostUSD)
	}
}

func TestSummarizeAggregatesAcrossWindow(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("a", base, "gpt-4o", 100, 50, 0.001),
		mkPrompt("b", base.Add(time.Hour), "gpt-4o", 200, 100, 0.002),
	}
	_ = store.AppendBatch(ctx, envs)
	agg := New(store, nil)
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.Requests != 2 || s.InputTokens != 300 || s.OutputTokens != 150 {
		t.Errorf("summary unexpected: %+v", s)
	}
	if s.CostUSD < 0.0029 || s.CostUSD > 0.0031 {
		t.Errorf("cost = %f", s.CostUSD)
	}
}

func TestFilterByWorkflow(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		{
			ID: "a", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: base, Source: "test",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
				InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CostUSD: 0.01,
				WorkflowID: "wf-A",
			},
		},
		{
			ID: "b", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: base, Source: "test",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
				InputTokens: 20, OutputTokens: 5, TotalTokens: 25, CostUSD: 0.02,
				WorkflowID: "wf-B",
			},
		},
	}
	_ = store.AppendBatch(ctx, envs)
	agg := New(store, nil)
	s, err := agg.Summarize(ctx, Filter{WorkflowID: "wf-A"})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.Requests != 1 || s.InputTokens != 10 {
		t.Errorf("filtered summary unexpected: %+v", s)
	}
}

func TestEmptyStoreReturnsEmpty(t *testing.T) {
	store := newStore(t)
	agg := New(store, nil)
	rows, err := agg.AggregateBy(context.Background(), Filter{}, BucketHour, GroupNone)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty, got %+v", rows)
	}
	s, err := agg.Summarize(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.Requests != 0 {
		t.Errorf("requests = %d, want 0", s.Requests)
	}
}

func TestDayBucketAlignment(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	d1 := time.Date(2026, 5, 9, 23, 30, 0, 0, time.UTC)
	d2 := time.Date(2026, 5, 10, 0, 30, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("a", d1, "gpt-4o", 100, 0, 0.01),
		mkPrompt("b", d2, "gpt-4o", 100, 0, 0.01),
	}
	_ = store.AppendBatch(ctx, envs)
	agg := New(store, nil)
	rows, err := agg.AggregateBy(ctx, Filter{}, BucketDay, GroupNone)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 daily buckets, got %d: %+v", len(rows), rows)
	}
	if rows[1].BucketStart.Sub(rows[0].BucketStart) != 24*time.Hour {
		t.Errorf("daily buckets not 24h apart: %s / %s", rows[0].BucketStart, rows[1].BucketStart)
	}
}

func TestFilterTimeWindow(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("early", base.Add(-time.Hour), "gpt-4o", 10, 0, 0.01),
		mkPrompt("mid", base.Add(time.Minute), "gpt-4o", 20, 0, 0.02),
		mkPrompt("late", base.Add(3*time.Hour), "gpt-4o", 30, 0, 0.03),
	}
	_ = store.AppendBatch(ctx, envs)
	agg := New(store, nil)
	s, err := agg.Summarize(ctx, Filter{
		Since: base, Until: base.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.Requests != 1 || s.InputTokens != 20 {
		t.Errorf("window summary: %+v", s)
	}
}
