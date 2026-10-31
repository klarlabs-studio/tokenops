package focus_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
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
	if err := focus.V1_2.WriteCSV(&buf, focus.V1_2.Build(usage, purchases)); err != nil {
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

// Allocation tags are the operator's own, one user-defined tag scheme, so
// their keys go unprefixed (FOCUS 1.2, Tags); untagged usage is null.
func TestAllocationTagsFillTheTagsColumn(t *testing.T) {
	day := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	rows := focus.V1_2.Build([]focus.Usage{
		{Day: day, Provider: "openai", Model: "gpt-5", Requests: 1, Tokens: 10, BilledUSD: 1, ListUSD: 1,
			Tags: map[string]string{"cost_center": "cc-42", "client": "Acme Corp"}},
		{Day: day, Provider: "openai", Model: "gpt-5", Requests: 2, Tokens: 20, BilledUSD: 2, ListUSD: 2},
	}, nil)
	if len(rows) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Tags != "" || rows[1].Tags != `{"client":"Acme Corp","cost_center":"cc-42"}` {
		t.Errorf("tags = %q, %q", rows[0].Tags, rows[1].Tags)
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
	rows := focus.V1_2.Build([]focus.Usage{{
		Day: day, Provider: "anthropic", Model: "claude-opus-5", PlanCovered: true,
		Plan: focus.Plan{ID: "claude-max", Display: "Claude Max"}, Requests: 4, Tokens: 2_500_000, ListUSD: 37.5,
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
	rec := focus.V1_2.Record(r)
	if got := rec[colIndex(t, "PricingQuantity")]; got != "2.5" {
		t.Errorf("PricingQuantity in millions of tokens: %q", got)
	}
	if got := rec[colIndex(t, "PricingUnit")]; got != "1000000 Tokens" {
		t.Errorf("PricingUnit: %q", got)
	}
	if r.ServiceName != "Claude Max" || r.SkuID != "claude-opus-5" || r.ProviderName != "Anthropic" {
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
	r := focus.V1_2.Build([]focus.Usage{{
		Day: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), Provider: "openai", Model: "gpt-5.5",
		Requests: 2, Tokens: 1000, BilledUSD: 0.42, ListUSD: 0.42,
	}}, nil)[0]
	if r.BilledCost != 0.42 || r.EffectiveCost != 0.42 || r.ContractedCost != 0.42 || r.ListCost != 0.42 {
		t.Errorf("costs: %+v", r)
	}
	if r.BillingAccountID == "" || r.BillingAccountID == focus.V1_2.Build([]focus.Usage{{
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
	rows := focus.V1_2.Build(nil, []focus.Purchase{{
		Provider: "anthropic", Plan: focus.Plan{ID: "claude-max", Display: "Claude Max"},
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
	if sep.ChargeFrequency != "Recurring" || sep.ConsumedQuantity != nil || sep.SkuID != "claude-max" {
		t.Errorf("frequency %q consumed %v sku %q", sep.ChargeFrequency, sep.ConsumedQuantity, sep.SkuID)
	}
	if got := focus.V1_2.Record(sep)[colIndex(t, "PricingUnit")]; got != "Months" {
		t.Errorf("PricingUnit %q", got)
	}
}

func colIndex(t *testing.T, name string) int {
	t.Helper()
	for i, c := range focus.V1_2.Columns {
		if c == name {
			return i
		}
	}
	t.Fatalf("no column %s", name)
	return -1
}

// TestExport14Golden pins the FOCUS 1.4 CSV byte for byte.
func TestExport14Golden(t *testing.T) {
	usage, purchases := sampleDataset14()
	var buf bytes.Buffer
	if err := focus.V1_4.WriteCSV(&buf, focus.V1_4.Build(usage, purchases)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "export-1.4.golden.csv")
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

// In FOCUS 1.4 TokenOps bills nothing: each kind of token is a row at its
// list rate, the estimate is split across them by list price and adds up
// to what the charge cost.
func TestMetered14UsageSplitsTheEstimateByKind(t *testing.T) {
	rows := focus.V1_4.Build([]focus.Usage{{
		Day: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), Provider: "openai", Model: "gpt-5.5",
		Requests: 2, Tokens: 3_000_000, BilledUSD: 30, ListUSD: 30,
		Priced: []focus.TokenPrice{
			{Kind: focus.TokensInput, Tokens: 2_000_000, PerMillion: 5},
			{Kind: focus.TokensCacheRead, Tokens: 0, PerMillion: 0.5},
			{Kind: focus.TokensOutput, Tokens: 1_000_000, PerMillion: 10},
		},
	}}, nil)
	if len(rows) != 2 {
		t.Fatalf("a row per kind with tokens, got %d: %+v", len(rows), rows)
	}
	in, out := rows[0], rows[1]
	if in.SkuPriceID != "gpt-5.5/input" || out.SkuPriceID != "gpt-5.5/output" || in.SkuID != "gpt-5.5" {
		t.Errorf("sku price ids %q, %q (sku %q)", in.SkuPriceID, out.SkuPriceID, in.SkuID)
	}
	if in.ListCost != 10 || out.ListCost != 10 || *in.ListUnitPrice != 5 || *out.ContractedUnitPrice != 10 {
		t.Errorf("list costs %v + %v at %v and %v", in.ListCost, out.ListCost, *in.ListUnitPrice, *out.ContractedUnitPrice)
	}
	if in.EstimatedCost != 15 || out.EstimatedCost != 15 {
		t.Errorf("the 30 estimated splits by list price: %v + %v", in.EstimatedCost, out.EstimatedCost)
	}
	for _, r := range rows {
		if r.BilledCost != 0 || r.EffectiveCost != 0 {
			t.Errorf("TokenOps issues no invoice, so it bills nothing: %+v", r)
		}
		if r.ProviderName != "OpenAI" || r.HostProviderName != "OpenAI" {
			t.Errorf("service provider %q, host %q", r.ProviderName, r.HostProviderName)
		}
	}
}

// Usage a plan covered is contracted at 0 against its list rate: the value
// the plan gave, with no estimate of its own.
func TestPlanCovered14UsageIsContractedAtZero(t *testing.T) {
	rows := focus.V1_4.Build([]focus.Usage{{
		Day: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), Provider: "anthropic", Model: "claude-opus-5",
		PlanCovered: true, Plan: focus.Plan{ID: "claude-max", Display: "Claude Max"}, Requests: 1, Tokens: 1_000_000,
		Priced: []focus.TokenPrice{{Kind: focus.TokensOutput, Tokens: 1_000_000, PerMillion: 25}},
	}}, nil)
	r := rows[0]
	if r.ListCost != 25 || r.ContractedCost != 0 || *r.ContractedUnitPrice != 0 || r.EstimatedCost != 0 {
		t.Errorf("list %v, contracted %v at %v, estimate %v", r.ListCost, r.ContractedCost, *r.ContractedUnitPrice, r.EstimatedCost)
	}
}

// A model without a rate stays one row: its unit prices are written as 0
// and the description says why, so no price is invented.
func TestUnpriced14UsageSaysItsPriceIsUnknown(t *testing.T) {
	rows := focus.V1_4.Build([]focus.Usage{{
		Day: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), Provider: "openai", Model: "gpt-next",
		Requests: 1, Tokens: 500, BilledUSD: 0.2, ListUSD: 0.2,
	}}, nil)
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	r := rows[0]
	if r.SkuPriceID != "gpt-next/tokens" || *r.ListUnitPrice != 0 || r.ListCost != 0 || r.EstimatedCost != 0.2 {
		t.Errorf("sku price %q, unit %v, list %v, estimate %v", r.SkuPriceID, *r.ListUnitPrice, r.ListCost, r.EstimatedCost)
	}
	if !strings.Contains(r.ChargeDescription, "no API rate is known") {
		t.Errorf("description: %q", r.ChargeDescription)
	}
}

func TestSpecForNamesTheSupportedVersions(t *testing.T) {
	if s, err := focus.SpecFor(""); err != nil || s.Version != "1.4" {
		t.Errorf("the default is 1.4: %v, %v", s.Version, err)
	}
	if s, err := focus.SpecFor("1.2"); err != nil || s.Version != "1.2" {
		t.Errorf("1.2: %v, %v", s.Version, err)
	}
	if _, err := focus.SpecFor("1.3"); err == nil {
		t.Error("an unsupported version must be refused")
	}
}
