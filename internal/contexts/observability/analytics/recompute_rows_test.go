package analytics

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A day bucket re-priced only the first hour of the day: the recompute
// window was fixed at one hour whatever the bucket, so a zero-cost event at
// 15:00 left its day costing nothing.
func TestAggregateRecomputesTheWholeDayBucket(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	day := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	_ = store.Append(ctx, mkPrompt("late", day.Add(15*time.Hour), "gpt-4o-mini", 1_000_000, 1_000_000, 0))

	rows, err := New(store, spend.NewEngine(spend.DefaultTable())).AggregateBy(ctx, Filter{}, BucketDay, GroupNone)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CostUSD < 0.7 || rows[0].CostRecomputed != 1 {
		t.Fatalf("day row = %+v, want the 15:00 event re-priced (~$0.75)", rows)
	}
}

// The recompute ignored the row's group, so in a per-model breakdown each
// unpriced model's row was charged for every model in the bucket.
func TestAggregateRecomputesEachGroupOnItsOwnEvents(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	at := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	_ = store.Append(ctx, mkPrompt("mini", at, "gpt-4o-mini", 1_000_000, 1_000_000, 0))
	_ = store.Append(ctx, mkPrompt("full", at.Add(time.Minute), "gpt-4o", 1_000_000, 1_000_000, 0))

	eng := spend.NewEngine(spend.DefaultTable())
	rows, err := New(store, eng).AggregateBy(ctx, Filter{}, BucketHour, GroupModel)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{}
	for _, m := range []string{"gpt-4o-mini", "gpt-4o"} {
		c, err := eng.Compute(&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: m, InputTokens: 1_000_000, OutputTokens: 1_000_000})
		if err != nil {
			t.Fatal(err)
		}
		want[m] = c
	}
	for _, r := range rows {
		if math.Abs(r.CostUSD-want[r.GroupKey]) > 1e-9 {
			t.Errorf("%s cost = %.4f, want %.4f — charged for other models' events", r.GroupKey, r.CostUSD, want[r.GroupKey])
		}
	}
}

// Rows and the summary are renderings of one window; a row whose stored
// cost was partly zero kept the zero events unpriced, so the rows summed to
// less than Summarize for the same window.
func TestAggregateRowsSumToSummarize(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	day := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	_ = store.Append(ctx, mkPrompt("priced", day.Add(2*time.Hour), "gpt-4o", 1_000, 1_000, 0.05))
	_ = store.Append(ctx, mkPrompt("unpriced", day.Add(9*time.Hour), "gpt-4o", 1_000_000, 1_000_000, 0))
	_ = store.Append(ctx, mkPrompt("other", day.Add(20*time.Hour), "gpt-4o-mini", 1_000_000, 0, 0))

	agg := New(store, spend.NewEngine(spend.DefaultTable()))
	sum, err := agg.Summarize(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []Bucket{BucketHour, BucketDay} {
		for _, g := range []Group{GroupNone, GroupModel} {
			rows, err := agg.AggregateBy(ctx, Filter{}, b, g)
			if err != nil {
				t.Fatal(err)
			}
			var total float64
			for _, r := range rows {
				total += r.CostUSD
			}
			if math.Abs(total-sum.CostUSD) > 1e-9 {
				t.Errorf("bucket=%s group=%q: rows sum to %.6f, Summarize says %.6f", b, g, total, sum.CostUSD)
			}
		}
	}
}
