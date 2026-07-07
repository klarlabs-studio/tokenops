package analytics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// vendor-usage-jsonl events ship token counts without cost (the JSONL
// has no rates). Summarize must recompute via spend.Engine so the
// dashboard's "TOTAL COST" tile isn't stuck at $0.00 the moment a user
// enables the live readers.
func TestSummarizeRecomputesMissingCost(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	_ = store.Append(ctx, mkPrompt("a", base, "gpt-4o-mini", 1_000_000, 1_000_000, 0))
	_ = store.Append(ctx, mkPrompt("b", base.Add(time.Minute), "gpt-4o-mini", 500_000, 500_000, 0))

	eng := spend.NewEngine(spend.DefaultTable())
	agg := New(store, eng)
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	// gpt-4o-mini @ 1.5M input + 1.5M output ≈ $0.225 + $0.90 = $1.125
	if s.CostUSD < 1.10 || s.CostUSD > 1.15 {
		t.Errorf("summary cost = %.4f; want ~1.125", s.CostUSD)
	}
	if s.Requests != 2 {
		t.Errorf("requests = %d", s.Requests)
	}
}

// Cache-heavy workloads (Claude Code JSONL) bundle cache_read into
// input_tokens but carry CachedInputTokens in payload. Recompute must
// pull the cached split out of the JSON payload so cache reads bill at
// the cheaper rate, not at the new-input rate.
func TestSummarizeUsesCachedInputFromPayload(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	env := &eventschema.Envelope{
		ID: "cache-heavy", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: base, Source: "claude-code-jsonl",
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-7",
			InputTokens: 1_000_000, CachedInputTokens: 990_000, OutputTokens: 10_000,
			TotalTokens: 1_010_000,
		},
	}
	_ = store.Append(ctx, env)

	eng := spend.NewEngine(spend.DefaultTable())
	agg := New(store, eng)
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	// claude-opus-4-7: $5/M input, $0.50/M cache, $25/M output
	//   uncached 10K   * 5/M     = 0.05
	//   cached 990K    * 0.50/M  = 0.495
	//   output 10K     * 25/M    = 0.25
	//   total ≈ 0.795
	if s.CostUSD < 0.75 || s.CostUSD > 0.85 {
		t.Errorf("cache-aware cost = %.4f; want ~0.795", s.CostUSD)
	}
	// Flat-rate (no cache awareness) would be: 1M * 5/M + 10K * 25/M = 5.25
	// Anything above ~2 means cache split is being ignored.
	if s.CostUSD > 2 {
		t.Errorf("cost %.4f looks like new-input rate is billing cache reads", s.CostUSD)
	}
}

// Mixed: some rows have stored cost, some don't. Summarize should add
// recomputed-from-tokens cost to the SUM(cost_usd) without double-
// counting events that already have a price.
func TestSummarizeMixedStoredAndRecomputedCost(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	// One event already priced (proxy-source style) — must be left alone.
	_ = store.Append(ctx, mkPrompt("priced", base, "gpt-4o-mini", 100, 50, 0.5))
	// One event missing cost (jsonl-source style) — recompute fills in.
	_ = store.Append(ctx, mkPrompt("free", base.Add(time.Minute), "gpt-4o-mini", 1_000_000, 1_000_000, 0))

	eng := spend.NewEngine(spend.DefaultTable())
	agg := New(store, eng)
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	// 0.5 stored + ~0.75 recomputed
	if s.CostUSD < 1.20 || s.CostUSD > 1.30 {
		t.Errorf("mixed summary cost = %.4f; want ~1.25", s.CostUSD)
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

// A model the pricing table doesn't know must surface in
// Summary.Unpriced instead of silently contributing zero cost — this is
// the operator's signal that a newly released model needs a rate.
func TestSummarizeReportsUnpricedModels(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		{
			ID: "known", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base, Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-6",
				InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100,
			},
		},
		{
			ID: "mystery-1", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(time.Minute), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-unreleased-9",
				InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100,
			},
		},
		{
			ID: "mystery-2", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(2 * time.Minute), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-unreleased-9",
				InputTokens: 500, OutputTokens: 50, TotalTokens: 550,
			},
		},
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}

	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.CostUSD <= 0 {
		t.Errorf("known model should still be costed; CostUSD = %.4f", s.CostUSD)
	}
	if len(s.Unpriced) != 1 {
		t.Fatalf("Unpriced = %+v; want exactly one entry", s.Unpriced)
	}
	u := s.Unpriced[0]
	if u.Provider != "anthropic" || u.Model != "claude-unreleased-9" || u.Requests != 2 {
		t.Errorf("Unpriced[0] = %+v; want anthropic/claude-unreleased-9 with 2 requests", u)
	}
}

// Priced models must never appear in Unpriced, even when their cost is
// recomputed from tokens.
func TestSummarizeNoUnpricedWhenAllModelsKnown(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	env := &eventschema.Envelope{
		ID: "known", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: base, Source: "claude-code-jsonl",
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-fable-5[1m]",
			InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100,
		},
	}
	if err := store.Append(ctx, env); err != nil {
		t.Fatalf("append: %v", err)
	}
	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(s.Unpriced) != 0 {
		t.Errorf("Unpriced = %+v; want empty", s.Unpriced)
	}
	if s.CostUSD <= 0 {
		t.Errorf("fable usage should be costed; CostUSD = %.4f", s.CostUSD)
	}
}

// Plan-included / trial events are zero-cost by design: recompute must
// not invent list-price spend for them, and their pseudo-models must
// not surface as unpriced.
func TestSummarizeSkipsPlanIncludedEvents(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		{
			ID: "plan-known", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base, Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-fable-5",
				InputTokens: 1_000_000, OutputTokens: 100_000, TotalTokens: 1_100_000,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		},
		{
			ID: "plan-pseudo", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(time.Minute), Source: "mcp-session",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "mcp-session",
				InputTokens: 100, TotalTokens: 100,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		},
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.CostUSD != 0 {
		t.Errorf("plan-included usage repriced at list rates: CostUSD = %.4f", s.CostUSD)
	}
	if len(s.Unpriced) != 0 {
		t.Errorf("plan-included pseudo-model flagged unpriced: %+v", s.Unpriced)
	}
}

// APIEquivalentUSD = real cost + list-price value of plan-covered
// traffic. Flat-plan deployments read their shadow value here while
// CostUSD stays honest at ~0.
func TestSummarizeAPIEquivalent(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		{
			ID: "plan", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base, Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-fable-5",
				InputTokens: 1_000_000, OutputTokens: 100_000, TotalTokens: 1_100_000,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		},
		{
			ID: "metered", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(time.Minute), Source: "proxy",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-haiku-4-5",
				InputTokens: 1_000_000, OutputTokens: 100_000, TotalTokens: 1_100_000,
			},
		},
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	s, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	// Real cost: haiku only — 1M×$1 + 100K×$5/M = 1.50
	if s.CostUSD < 1.45 || s.CostUSD > 1.55 {
		t.Errorf("CostUSD = %.4f; want ~1.50 (metered only)", s.CostUSD)
	}
	// Equivalent adds fable at list: 1M×$10 + 100K×$50/M = 15.00 → 16.50
	if s.APIEquivalentUSD < 16.40 || s.APIEquivalentUSD > 16.60 {
		t.Errorf("APIEquivalentUSD = %.4f; want ~16.50", s.APIEquivalentUSD)
	}
}
