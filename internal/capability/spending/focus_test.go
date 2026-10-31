package spending

import (
	"context"
	"maps"
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type chargesStub struct {
	got     Window
	charges []analytics.Charge
	// rates are the list rates by model; a model missing has none.
	rates map[string]spend.Rate
}

func (c *chargesStub) RateAt(_, model string, _ time.Time) (spend.Rate, bool) {
	r, ok := c.rates[model]
	return r, ok
}

func (c *chargesStub) DailyCharges(_ context.Context, w Window) ([]analytics.Charge, error) {
	c.got = w
	return c.charges, nil
}

func TestFocusExportMapsChargesAndPlanFees(t *testing.T) {
	sep3 := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	src := &chargesStub{charges: []analytics.Charge{
		{Day: sep3, Provider: "anthropic", Model: "claude-opus-5", PlanCovered: true, Requests: 3,
			InputTokens: 900_000, OutputTokens: 100_000, ListUSD: 15},
		{Day: sep3, Provider: "openai", Model: "gpt-5.5", Requests: 1, InputTokens: 100, OutputTokens: 10,
			CostUSD: 0.01, ListUSD: 0.01, Unpriced: true},
	}}
	// Pro until the 2nd, Max from then on, at the operator's own price.
	history := plans.History{
		{Provider: "anthropic", Plan: "claude-pro", From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{Provider: "anthropic", Plan: "claude-max", From: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Price: 90, Currency: "EUR"},
	}
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	got, err := FocusExport(context.Background(), src, FocusQuery{
		History: history, Plans: map[string]string{"anthropic": "claude-max"}, Now: now, Version: "1.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Since.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || !got.Until.Equal(now) {
		t.Errorf("the default window is the month so far: %s – %s", got.Since, got.Until)
	}
	if !src.got.Since.Equal(got.Since) || !src.got.Until.Equal(got.Until) {
		t.Errorf("charges read over %s – %s", src.got.Since, src.got.Until)
	}
	var usage, purchases []string
	for _, r := range got.Rows {
		line := r.ServiceName + " " + r.BillingCurrency
		switch r.ChargeCategory {
		case "Usage":
			usage = append(usage, line)
		case "Purchase":
			purchases = append(purchases, line+" "+r.ChargePeriodStart.Format("01-02"))
		}
	}
	if strings.Join(usage, "|") != "Claude Max USD|OpenAI API USD" {
		t.Errorf("usage rows name the plan in force that day: %v", usage)
	}
	if strings.Join(purchases, "|") != "Claude Pro USD 09-01|Claude Max EUR 09-02" {
		t.Errorf("each plan in force is a purchase in its own currency: %v", purchases)
	}
	for _, r := range got.Rows {
		if r.ServiceName == "Claude Max" && r.ChargeCategory == "Purchase" {
			want := plans.PeriodCost(90, plans.Period{From: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), To: now})
			if r.BilledCost != want {
				t.Errorf("Max fee %v, want %v prorated from the operator's price", r.BilledCost, want)
			}
		}
	}
	if len(got.Notes) == 0 || !strings.Contains(strings.Join(got.Notes, "\n"), "no API rate") {
		t.Errorf("an understated ListCost is noted: %v", got.Notes)
	}
}

func TestFocusExportRefusesAnEmptyWindow(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	_, err := FocusExport(context.Background(), &chargesStub{}, FocusQuery{Since: now, Until: now.Add(-time.Hour), Now: now})
	if err == nil {
		t.Fatal("a window that ends before it starts must be refused")
	}
}

// In FOCUS 1.4 each charge is priced per kind of token at the rate card of
// its day: the kinds' list prices add up to what the spend engine prices
// the charge at, cache reads and writes clamped to the input, and the
// estimate adds up to what the charge cost.
func TestFocusExport14PricesEachKindOfToken(t *testing.T) {
	sep3 := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	rate := spend.Rate{InputPerMillion: 5, OutputPerMillion: 25, CachedInputPerMillion: 0.5,
		CacheWritePerMillion: 6.25, CacheWrite1hPerMillion: 10}
	covered := analytics.Charge{Day: sep3, Provider: "anthropic", Model: "claude-opus-5", PlanCovered: true,
		Requests: 3, InputTokens: 1_000_000, CacheReadTokens: 600_000, CacheWriteTokens: 300_000,
		CacheWrite1hTokens: 100_000, OutputTokens: 50_000}
	metered := analytics.Charge{Day: sep3, Provider: "anthropic", Model: "claude-opus-5", Requests: 1,
		InputTokens: 2_000, OutputTokens: 1_000, CostUSD: 0.04, ListUSD: 0.04}
	unknown := analytics.Charge{Day: sep3, Provider: "openai", Model: "gpt-next", Requests: 1,
		InputTokens: 10, OutputTokens: 5, CostUSD: 0.001, ListUSD: 0.001}
	src := &chargesStub{charges: []analytics.Charge{covered, metered, unknown},
		rates: map[string]spend.Rate{"claude-opus-5": rate}}
	got, err := FocusExport(context.Background(), src, FocusQuery{Now: sep3.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.4" {
		t.Fatalf("the default version is 1.4, got %s", got.Version)
	}
	engine := spend.NewEngine(spend.Table{Rates: map[spend.Key]spend.Rate{
		{Provider: "anthropic", Model: "claude-opus-5"}: rate}})
	want, err := engine.Compute(&eventschema.PromptEvent{Provider: "anthropic", RequestModel: "claude-opus-5",
		InputTokens: covered.InputTokens, CachedInputTokens: covered.CacheReadTokens,
		CacheWriteInputTokens: covered.CacheWriteTokens, CacheWrite1hInputTokens: covered.CacheWrite1hTokens,
		OutputTokens: covered.OutputTokens})
	if err != nil {
		t.Fatal(err)
	}
	var list, estimate float64
	kinds := map[string]int64{}
	var unknownRows int
	for _, r := range got.Rows {
		if r.BilledCost != 0 || r.EffectiveCost != 0 {
			t.Errorf("TokenOps bills nothing in 1.4: %+v", r)
		}
		switch {
		case r.ChargeCategory != "Usage":
		case r.SkuID == "gpt-next":
			unknownRows++
		case r.ServiceName == "Claude Max" || r.BillingAccountID == "anthropic/subscription":
			list += r.ListCost
			kinds[r.SkuPriceID] += *r.ConsumedQuantity
		default:
			estimate += r.EstimatedCost
		}
	}
	if math.Abs(list-want) > 1e-9 {
		t.Errorf("the kinds' list prices add up to %v, the engine prices the charge at %v", list, want)
	}
	wantKinds := map[string]int64{"claude-opus-5/input": 100_000, "claude-opus-5/cache-read": 600_000,
		"claude-opus-5/cache-write-5m": 200_000, "claude-opus-5/cache-write-1h": 100_000, "claude-opus-5/output": 50_000}
	if !maps.Equal(kinds, wantKinds) {
		t.Errorf("tokens by kind %v, want %v", kinds, wantKinds)
	}
	if math.Abs(estimate-0.04) > 1e-12 {
		t.Errorf("the metered estimate adds up to %v, the charge cost 0.04", estimate)
	}
	if unknownRows != 1 {
		t.Errorf("a model without a rate is one row, got %d", unknownRows)
	}
}
