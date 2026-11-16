package headroom_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// With no window readings, the fee-versus-API advice prices the days the
// store holds events for: asked for 60 days with 28 days of history, it
// prices 28, not the usage spread over 60. The history starts at the
// first event of any provider, and a store that reaches back past the
// period prices the whole period.
func TestAdviseWithoutReadingsPricesTheDaysWithHistory(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	turn := func(id string, p eventschema.Provider, at time.Time) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: "claude-code-jsonl", Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload: &eventschema.PromptEvent{Provider: p, CostSource: eventschema.CostSourcePlanIncluded},
		}
	}
	day := 24 * time.Hour
	for _, tc := range []struct {
		name  string
		older []*eventschema.Envelope
		days  float64
	}{
		{"history starts 28 days back", nil, 28},
		{"another provider's history starts 40 days back", []*eventschema.Envelope{turn("o", eventschema.ProviderOpenAI, now.Add(-40*day))}, 40},
		{"history reaches back past the period", []*eventschema.Envelope{turn("o", eventschema.ProviderOpenAI, now.Add(-70*day))}, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			envs := append([]*eventschema.Envelope{}, tc.older...)
			for i := range 28 {
				envs = append(envs, turn(fmt.Sprintf("a%d", i), eventschema.ProviderAnthropic, now.Add(-time.Duration(28-i)*day+time.Minute)))
			}
			if err := store.AppendBatch(ctx, envs); err != nil {
				t.Fatal(err)
			}
			res, err := headroom.Advise(ctx, headroom.Deps{
				Config: cfgWith(map[string]string{"anthropic": "claude-max"}),
				Reader: storeReader{store},
				Price:  tenDollars,
			}, headroom.AdviceQuery{Days: 60}, now)
			if err != nil || len(res.Providers) != 1 {
				t.Fatalf("result = %+v, %v", res, err)
			}
			a := res.Providers[0]
			if math.Abs(a.Days-tc.days) > 0.05 {
				t.Errorf("days = %v, want %v", a.Days, tc.days)
			}
			var api *plans.Option
			for i := range a.Options {
				if a.Options[i].Kind == plans.OptionAPI {
					api = &a.Options[i]
				}
			}
			want := 280 * 30.44 / tc.days
			if api == nil || api.MonthlyUSD == nil || math.Abs(*api.MonthlyUSD-want) > 0.5 {
				t.Errorf("api = %+v, want $%.2f a month", api, want)
			}
		})
	}
}
