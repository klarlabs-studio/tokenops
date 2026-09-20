package analytics

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func meteredEvent(id, model string, in, out int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID:            id,
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC().Add(-time.Hour),
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: model,
			InputTokens: in, OutputTokens: out, TotalTokens: in + out,
			CostSource: eventschema.CostSourceMetered,
		},
	}
}

// Summarize reports an unpriced model as a gap. AggregateBy skips it
// with `if cerr != nil { continue }` and says nothing — and AggregateBy
// is what feeds burn rate, forecast, top consumers and the dashboard.
//
// The cost is therefore understated by whatever the unpriced model
// consumed, on exactly the surfaces an operator acts on, with the figure
// presenting itself as complete.
func TestAggregateByReportsTheUnpricedGap(t *testing.T) {
	st := storeWith(t,
		meteredEvent("a", "claude-sonnet-4-6", 1_000_000, 100_000),
		meteredEvent("b", "totally-unknown-model", 5_000_000, 500_000),
	)
	agg := New(st, spend.NewEngine(spend.DefaultTable()))

	rows, err := agg.AggregateBy(context.Background(),
		Filter{Since: time.Now().Add(-24 * time.Hour)}, BucketDay, GroupNone)
	if err != nil {
		t.Fatalf("AggregateBy: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}

	var total measurement.Value
	for _, r := range rows {
		total = measurement.Sum(total, r.Cost)
	}
	if total.Complete() {
		t.Error("the total reported itself complete while a model had no rate card")
	}
	if c := total.Coverage(); c.Excluded == 0 {
		t.Errorf("no events were reported as excluded: %+v", c)
	}
	if !strings.Contains(strings.Join(total.Coverage().Reasons, " "), "totally-unknown-model") {
		t.Errorf("the gap does not name the model: %+v", total.Coverage().Reasons)
	}
}

// A fully priced window must report itself complete, or the warning
// becomes noise every operator learns to ignore.
func TestAggregateByReportsNoGapWhenEverythingIsPriced(t *testing.T) {
	st := storeWith(t, meteredEvent("a", "claude-sonnet-4-6", 1_000_000, 100_000))
	agg := New(st, spend.NewEngine(spend.DefaultTable()))

	rows, err := agg.AggregateBy(context.Background(),
		Filter{Since: time.Now().Add(-24 * time.Hour)}, BucketDay, GroupNone)
	if err != nil {
		t.Fatalf("AggregateBy: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if !r.Cost.Complete() {
			t.Errorf("a fully priced row reported a gap: %+v", r.Cost.Coverage())
		}
		if r.Cost.Quality() == measurement.QualityUnknown {
			t.Errorf("a priced row reported unknown cost: %+v", r.Cost)
		}
	}
}

// The provenance field and the plain float must agree, so a consumer
// that has not migrated yet still sees the number it always saw.
func TestRowCostMatchesTheLegacyFloat(t *testing.T) {
	st := storeWith(t,
		meteredEvent("a", "claude-sonnet-4-6", 1_000_000, 100_000),
		meteredEvent("b", "claude-haiku-4-5", 2_000_000, 200_000),
	)
	agg := New(st, spend.NewEngine(spend.DefaultTable()))

	rows, err := agg.AggregateBy(context.Background(),
		Filter{Since: time.Now().Add(-24 * time.Hour)}, BucketDay, GroupNone)
	if err != nil {
		t.Fatalf("AggregateBy: %v", err)
	}
	for _, r := range rows {
		if got := r.Cost.AmountOr(-1); got != r.CostUSD {
			t.Errorf("Cost = %v but CostUSD = %v", got, r.CostUSD)
		}
	}
}

// Recomputed cost is derived from a rate card, not observed. Saying so
// is the difference between "this is what you were billed" and "this is
// what we worked out you were billed".
func TestRecomputedCostIsDerivedNotMeasured(t *testing.T) {
	st := storeWith(t, meteredEvent("a", "claude-sonnet-4-6", 1_000_000, 100_000))
	agg := New(st, spend.NewEngine(spend.DefaultTable()))

	rows, err := agg.AggregateBy(context.Background(),
		Filter{Since: time.Now().Add(-24 * time.Hour)}, BucketDay, GroupNone)
	if err != nil {
		t.Fatalf("AggregateBy: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.CostRecomputed == 0 {
			continue
		}
		if q := r.Cost.Quality(); q != measurement.QualityDerived {
			t.Errorf("a recomputed row reports quality %q, want derived", q)
		}
	}
}

// Without a spend engine nothing can price a zero-cost row. That is not
// a cost of zero — it is no answer, and the difference decides whether a
// dashboard shows "$0.00" or "pricing unavailable".
func TestNoSpendEngineReportsUnknownRatherThanZero(t *testing.T) {
	st := storeWith(t, meteredEvent("a", "claude-sonnet-4-6", 1_000_000, 100_000))
	agg := New(st, nil)

	rows, err := agg.AggregateBy(context.Background(),
		Filter{Since: time.Now().Add(-24 * time.Hour)}, BucketDay, GroupNone)
	if err != nil {
		t.Fatalf("AggregateBy: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.CostUSD != 0 {
			continue // the store had a cost; nothing needed recomputing
		}
		if r.Cost.Known() {
			t.Errorf("an unpriceable row reported a known cost of %v", r.Cost.AmountOr(-1))
		}
		if !strings.Contains(r.Cost.Caveat(), "pricing") {
			t.Errorf("the caveat does not explain why: %q", r.Cost.Caveat())
		}
	}
}
