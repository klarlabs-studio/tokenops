package analytics

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// seedPlanCoveredMix writes two models of plan-covered traffic plus one
// metered row, which is the shape a flat-rate operator's store actually has.
func seedPlanCoveredMix(t *testing.T, store interface {
	AppendBatch(context.Context, []*eventschema.Envelope) error
}, base time.Time) {
	t.Helper()
	envs := []*eventschema.Envelope{
		{
			ID: "plan-opus", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base, Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-5",
				InputTokens: 2_000_000, OutputTokens: 200_000, TotalTokens: 2_200_000,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		},
		{
			ID: "plan-sonnet", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(time.Minute), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5",
				InputTokens: 500_000, OutputTokens: 50_000, TotalTokens: 550_000,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		},
	}
	if err := store.AppendBatch(context.Background(), envs); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// The headline said $3633 while every per-model row read $0.0000, so the
// table that exists to rank consumers ranked them all equal. Rows must carry
// the same shadow value the summary reports.
func TestAggregateByCarriesAPIEquivalentPerGroup(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	seedPlanCoveredMix(t, store, base)

	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	rows, err := agg.AggregateBy(ctx, Filter{}, BucketDay, GroupModel)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 model rows, got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.CostUSD != 0 {
			t.Errorf("%s: plan-covered traffic must stay $0 real: %.4f", r.GroupKey, r.CostUSD)
		}
		if r.APIEquivalentUSD <= 0 {
			t.Errorf("%s: want a list-price equivalent, got %.4f", r.GroupKey, r.APIEquivalentUSD)
		}
	}
}

// The invariant Daria's report actually exposed: the headline and the rows
// are two renderings of one window, and they disagreed.
func TestGroupEquivalentsSumToTheSummary(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	seedPlanCoveredMix(t, store, base)

	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	summary, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	rows, err := agg.AggregateBy(ctx, Filter{}, BucketDay, GroupModel)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	var total float64
	for _, r := range rows {
		total += r.APIEquivalentUSD
	}
	if math.Abs(total-summary.APIEquivalentUSD) > 0.0001 {
		t.Fatalf("rows sum to %.4f but the headline says %.4f", total, summary.APIEquivalentUSD)
	}
}

// Metered traffic already has a real cost; the equivalent must equal it
// rather than double-counting it as shadow value on top.
func TestMeteredRowEquivalentEqualsItsCost(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{{
		ID: "metered", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: base, Source: "proxy",
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-5",
			InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100,
			CostUSD: 1.25, CostSource: eventschema.CostSourceMetered,
		},
	}}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	rows, err := agg.AggregateBy(ctx, Filter{}, BucketDay, GroupModel)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if math.Abs(rows[0].APIEquivalentUSD-rows[0].CostUSD) > 0.0001 {
		t.Fatalf("metered equivalent %.4f != cost %.4f", rows[0].APIEquivalentUSD, rows[0].CostUSD)
	}
}
