package focus_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/focus"
)

var update = flag.Bool("update", false, "rewrite testdata goldens")

// TestExportGolden pins the CSV byte for byte: what a FinOps tool ingests
// must not change by accident.
func TestExportGolden(t *testing.T) {
	usage, purchases := sampleDataset()
	var buf bytes.Buffer
	if err := focus.WriteCSV(&buf, focus.Build(usage, purchases)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "export.golden.csv")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update)", err)
	}
	if !bytes.Equal(want, buf.Bytes()) {
		t.Errorf("the export changed:\n--- want\n%s\n--- got\n%s", want, buf.Bytes())
	}
}

func byCategory(rows []focus.Row, category string) []focus.Row {
	var out []focus.Row
	for _, r := range rows {
		if r.ChargeCategory == category {
			out = append(out, r)
		}
	}
	return out
}

// Plan-included usage is billed nothing and valued at the API price:
// FOCUS's ListCost against EffectiveCost is "value per plan".
func TestPlanCoveredUsageIsFreeAtListPrice(t *testing.T) {
	day := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	rows := focus.Build([]focus.Usage{{
		Day: day, Provider: "anthropic", Model: "claude-opus-5", PlanCovered: true,
		Plan: focus.Plan{ID: "claude-max-5x", Display: "Claude Max 5x"}, Requests: 4, Tokens: 2_500_000, ListUSD: 37.5,
	}}, nil)
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	r := rows[0]
	if r.ChargeCategory != "Usage" || r.ChargeFrequency != "Usage-Based" {
		t.Errorf("category %q, frequency %q", r.ChargeCategory, r.ChargeFrequency)
	}
	if r.BilledCost != 0 || r.EffectiveCost != 0 || r.ContractedCost != 0 || r.ListCost != 37.5 {
		t.Errorf("costs: billed %v effective %v contracted %v list %v", r.BilledCost, r.EffectiveCost, r.ContractedCost, r.ListCost)
	}
	if r.ConsumedQuantity == nil || *r.ConsumedQuantity != 2_500_000 || r.ConsumedUnit != "Tokens" {
		t.Errorf("consumed: %v %q", r.ConsumedQuantity, r.ConsumedUnit)
	}
	rec := r.Record()
	if got := rec[colIndex(t, "PricingQuantity")]; got != "2.5" {
		t.Errorf("PricingQuantity in millions of tokens: %q", got)
	}
	if got := rec[colIndex(t, "PricingUnit")]; got != "1000000 Tokens" {
		t.Errorf("PricingUnit: %q", got)
	}
	if r.ServiceName != "Claude Max 5x" || r.SkuID != "claude-opus-5" || r.ProviderName != "Anthropic" {
		t.Errorf("service %q, sku %q, provider %q", r.ServiceName, r.SkuID, r.ProviderName)
	}
	if r.ServiceCategory != "AI and Machine Learning" || r.ServiceSubcategory != "Generative AI" {
		t.Errorf("category %q / %q", r.ServiceCategory, r.ServiceSubcategory)
	}
	if !r.ChargePeriodStart.Equal(day) || !r.ChargePeriodEnd.Equal(day.Add(24*time.Hour)) {
		t.Errorf("charge period %s–%s", r.ChargePeriodStart, r.ChargePeriodEnd)
	}
	if !r.BillingPeriodStart.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || !r.BillingPeriodEnd.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("billing period %s–%s", r.BillingPeriodStart, r.BillingPeriodEnd)
	}
	if r.Tags != "" || r.ChargeClass != "" {
		t.Errorf("tags %q and charge class %q are null until there is something to say", r.Tags, r.ChargeClass)
	}
}

// Usage billed per token costs what it cost, on every cost column.
func TestMeteredUsageCostsWhatItCost(t *testing.T) {
	r := focus.Build([]focus.Usage{{
		Day: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), Provider: "openai", Model: "gpt-5.5",
		Requests: 2, Tokens: 1000, BilledUSD: 0.42, ListUSD: 0.42,
	}}, nil)[0]
	if r.BilledCost != 0.42 || r.EffectiveCost != 0.42 || r.ContractedCost != 0.42 || r.ListCost != 0.42 {
		t.Errorf("costs: %+v", r)
	}
	if r.BillingAccountID == "" || r.BillingAccountID == focus.Build([]focus.Usage{{
		Day: r.ChargePeriodStart, Provider: "openai", Model: "gpt-5.5", PlanCovered: true, Requests: 1, Tokens: 1,
	}}, nil)[0].BillingAccountID {
		t.Errorf("per-token usage and the subscription are different billing accounts: %q", r.BillingAccountID)
	}
	if r.ProviderName != "OpenAI" || r.ServiceName != "OpenAI API" {
		t.Errorf("provider %q, service %q", r.ProviderName, r.ServiceName)
	}
}

// A plan fee is a Purchase, split at month boundaries so each charge sits
// inside one billing period, prorated by time.
func TestPlanFeesArePurchasesSplitByBillingPeriod(t *testing.T) {
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	rows := focus.Build(nil, []focus.Purchase{{
		Provider: "anthropic", Plan: focus.Plan{ID: "claude-max-5x", Display: "Claude Max 5x"},
		From: from, To: to, Amount: 20, Currency: "USD", Months: 0.6,
	}})
	purchases := byCategory(rows, "Purchase")
	if len(purchases) != 2 {
		t.Fatalf("want the plan split into September and October, got %+v", purchases)
	}
	sep, oct := purchases[0], purchases[1]
	if !sep.ChargePeriodEnd.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !oct.ChargePeriodStart.Equal(sep.ChargePeriodEnd) {
		t.Errorf("split at the month: %s / %s", sep.ChargePeriodEnd, oct.ChargePeriodStart)
	}
	if sep.BilledCost != 10 || oct.BilledCost != 10 {
		t.Errorf("ten days each of twenty: %v + %v", sep.BilledCost, oct.BilledCost)
	}
	if sep.EffectiveCost != sep.BilledCost || sep.ListCost != sep.BilledCost {
		t.Errorf("a subscription is not a commitment discount: its effective cost is what it costs: %+v", sep)
	}
	if sep.ChargeFrequency != "Recurring" || sep.ConsumedQuantity != nil || sep.SkuID != "claude-max-5x" {
		t.Errorf("frequency %q consumed %v sku %q", sep.ChargeFrequency, sep.ConsumedQuantity, sep.SkuID)
	}
	if got := sep.Record()[colIndex(t, "PricingUnit")]; got != "Months" {
		t.Errorf("PricingUnit %q", got)
	}
}

func colIndex(t *testing.T, name string) int {
	t.Helper()
	for i, c := range focus.Columns {
		if c == name {
			return i
		}
	}
	t.Fatalf("no column %s", name)
	return -1
}
