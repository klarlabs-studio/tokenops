package analytics_test

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func usageEvent(id string, at time.Time, provider eventschema.Provider, model string, in, out int64, cost float64, source eventschema.CostSource) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: at, Source: "test",
		Payload: &eventschema.PromptEvent{
			Provider: provider, RequestModel: model,
			InputTokens: in, OutputTokens: out, TotalTokens: in + out,
			CostUSD: cost, CostSource: source,
		},
	}
}

// DailyCharges splits each day's usage of one provider's model into what a
// plan covered and what was billed per token, because a FOCUS export
// reports the two as different charges: one billed $0 with an API list
// price, the other billed what it cost.
func TestDailyChargesSplitsPlanCoveredFromMetered(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		usageEvent("p1", day.Add(9*time.Hour), eventschema.ProviderAnthropic, "claude-opus-5", 2_000_000, 200_000, 0, eventschema.CostSourcePlanIncluded),
		usageEvent("p2", day.Add(10*time.Hour), eventschema.ProviderAnthropic, "claude-opus-5", 1_000_000, 100_000, 0, eventschema.CostSourcePlanIncluded),
		usageEvent("m1", day.Add(11*time.Hour), eventschema.ProviderAnthropic, "claude-opus-5", 1000, 100, 1.25, eventschema.CostSourceMetered),
		usageEvent("o1", day.Add(12*time.Hour), eventschema.ProviderOpenAI, "gpt-4o", 1000, 500, 0.02, eventschema.CostSourceMetered),
		usageEvent("next", day.Add(30*time.Hour), eventschema.ProviderAnthropic, "claude-opus-5", 500, 50, 0, eventschema.CostSourcePlanIncluded),
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	agg := analytics.New(store, spend.NewEngine(spend.DefaultTable()))
	charges, err := agg.DailyCharges(ctx, analytics.Filter{})
	if err != nil {
		t.Fatalf("charges: %v", err)
	}
	if len(charges) != 4 {
		t.Fatalf("want 4 charges (plan + metered opus, metered gpt, next day's plan), got %d: %+v", len(charges), charges)
	}
	byKey := map[string]analytics.Charge{}
	for _, c := range charges {
		k := c.Day.Format("2006-01-02") + "/" + c.Provider + "/" + c.Model
		if c.PlanCovered {
			k += "/plan"
		}
		byKey[k] = c
	}
	plan := byKey["2026-09-03/anthropic/claude-opus-5/plan"]
	if plan.Requests != 2 || plan.InputTokens != 3_000_000 || plan.OutputTokens != 300_000 {
		t.Errorf("plan-covered usage: %+v", plan)
	}
	if plan.CostUSD != 0 || plan.ListUSD <= 0 || plan.Unpriced {
		t.Errorf("plan-covered usage bills $0 and is valued at list: %+v", plan)
	}
	metered := byKey["2026-09-03/anthropic/claude-opus-5"]
	if metered.Requests != 1 || metered.InputTokens != 1000 || metered.OutputTokens != 100 {
		t.Errorf("metered usage: %+v", metered)
	}
	if metered.CostUSD != 1.25 || metered.ListUSD != 1.25 {
		t.Errorf("metered usage bills its stored cost, at list: %+v", metered)
	}
	if got := byKey["2026-09-03/openai/gpt-4o"]; got.CostUSD != 0.02 || got.Requests != 1 {
		t.Errorf("openai metered: %+v", got)
	}
	if got := byKey["2026-09-04/anthropic/claude-opus-5/plan"]; got.Requests != 1 || !got.Day.Equal(day.Add(24*time.Hour)) {
		t.Errorf("next day's charge: %+v", got)
	}

	// The charges are the same window the summary reports.
	sum, err := agg.Summarize(ctx, analytics.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var cost, list float64
	var requests int64
	for _, c := range charges {
		cost += c.CostUSD
		list += c.ListUSD
		requests += c.Requests
	}
	if requests != sum.Requests || math.Abs(cost-sum.CostUSD) > 1e-9 || math.Abs(list-sum.APIEquivalentUSD) > 1e-6 {
		t.Errorf("charges sum to %d requests, $%.6f billed, $%.6f list; the summary says %d, $%.6f, $%.6f",
			requests, cost, list, sum.Requests, sum.CostUSD, sum.APIEquivalentUSD)
	}
}

// A model with no rate card cannot be valued at list price; the charge
// says so instead of presenting $0 as its value.
func TestDailyChargesFlagsUnpricedUsage(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{
		usageEvent("p", day.Add(time.Hour), eventschema.ProviderAnthropic, "claude-unheard-of", 1000, 100, 0, eventschema.CostSourcePlanIncluded),
		usageEvent("m", day.Add(2*time.Hour), eventschema.ProviderOpenAI, "gpt-unheard-of", 1000, 100, 0, eventschema.CostSourceMetered),
	}); err != nil {
		t.Fatal(err)
	}
	charges, err := analytics.New(store, spend.NewEngine(spend.DefaultTable())).DailyCharges(ctx, analytics.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(charges) != 2 {
		t.Fatalf("want 2 charges, got %+v", charges)
	}
	for _, c := range charges {
		if !c.Unpriced {
			t.Errorf("%s/%s has no rate card and must say so: %+v", c.Provider, c.Model, c)
		}
	}
}
