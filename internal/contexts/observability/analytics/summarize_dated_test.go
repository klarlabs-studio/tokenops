package analytics

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Summarize must price each event at the rate card in effect when it
// happened. Pricing an aggregate with no timestamp selects the OLDEST
// card, so a model added (or repriced) by a later refresh would be
// reported as unpriced, or valued at its old price.
func TestSummarizePricesAtTheEventsRateCard(t *testing.T) {
	now := time.Now().UTC()
	key := func(model string) spend.Key {
		return spend.Key{Provider: eventschema.ProviderAnthropic, Model: model}
	}
	engine := spend.NewDatedEngine([]spend.DatedTable{
		{EffectiveFrom: now.Add(-30 * 24 * time.Hour), Table: spend.Table{Rates: map[spend.Key]spend.Rate{
			key("repriced"): {InputPerMillion: 1, OutputPerMillion: 1},
		}}},
		{EffectiveFrom: now.Add(-7 * 24 * time.Hour), Table: spend.Table{Rates: map[spend.Key]spend.Rate{
			key("repriced"):  {InputPerMillion: 10, OutputPerMillion: 10},
			key("new-model"): {InputPerMillion: 10, OutputPerMillion: 10},
		}}},
	})
	metered := func(id, model string) *eventschema.Envelope {
		env := planEvent(id, model, 1_000_000, 0)
		env.Payload.(*eventschema.PromptEvent).CostSource = eventschema.CostSourceMetered
		return env
	}

	for _, tc := range []struct {
		name  string
		event *eventschema.Envelope
		total func(Summary) float64
	}{
		{"plan-covered value, repriced model", planEvent("a", "repriced", 1_000_000, 0), func(s Summary) float64 { return s.APIEquivalentUSD }},
		{"plan-covered value, model added later", planEvent("b", "new-model", 1_000_000, 0), func(s Summary) float64 { return s.APIEquivalentUSD }},
		{"recomputed cost, repriced model", metered("c", "repriced"), func(s Summary) float64 { return s.CostUSD }},
		{"recomputed cost, model added later", metered("d", "new-model"), func(s Summary) float64 { return s.CostUSD }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agg := New(storeWith(t, tc.event), engine)
			got, err := agg.Summarize(context.Background(), Filter{Since: now.Add(-24 * time.Hour)})
			if err != nil {
				t.Fatalf("summarize: %v", err)
			}
			if len(got.Unpriced) != 0 {
				t.Errorf("Unpriced = %+v, want none", got.Unpriced)
			}
			if total := tc.total(got); total != 10 {
				t.Errorf("total = %v, want 10 (current card)", total)
			}
		})
	}
}
