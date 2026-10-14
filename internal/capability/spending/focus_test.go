package spending

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

type chargesStub struct {
	got     Window
	charges []analytics.Charge
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
	// Pro until the 2nd, Max 5x from then on, at the operator's own price.
	history := plans.History{
		{Provider: "anthropic", Plan: "claude-pro", From: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{Provider: "anthropic", Plan: "claude-max-5x", From: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Price: 90, Currency: "EUR"},
	}
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	got, err := FocusExport(context.Background(), src, FocusQuery{
		History: history, Plans: map[string]string{"anthropic": "claude-max-5x"}, Now: now,
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
	if strings.Join(usage, "|") != "Claude Max 5x USD|OpenAI API USD" {
		t.Errorf("usage rows name the plan in force that day: %v", usage)
	}
	if strings.Join(purchases, "|") != "Claude Pro USD 09-01|Claude Max 5x EUR 09-02" {
		t.Errorf("each plan in force is a purchase in its own currency: %v", purchases)
	}
	for _, r := range got.Rows {
		if r.ServiceName == "Claude Max 5x" && r.ChargeCategory == "Purchase" {
			want := plans.PeriodCost(90, plans.Period{From: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), To: now})
			if r.BilledCost != want {
				t.Errorf("Max 5x fee %v, want %v prorated from the operator's price", r.BilledCost, want)
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
