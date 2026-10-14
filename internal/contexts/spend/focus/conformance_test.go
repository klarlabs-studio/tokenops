package focus_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/focus"
)

// The rules below are transcribed from the FOCUS 1.2 specification
// (github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec, tag v1.2,
// specification/columns/*.md and specification/attributes/*.md), not
// from the package under test, so a change to the export that breaks
// conformance fails here.

// specMandatory are the columns FOCUS 1.2 marks Mandatory: every FOCUS
// dataset carries them.
var specMandatory = []string{
	"BilledCost", "BillingAccountId", "BillingAccountName", "BillingCurrency",
	"BillingPeriodEnd", "BillingPeriodStart", "ChargeCategory", "ChargeClass",
	"ChargeDescription", "ChargePeriodEnd", "ChargePeriodStart", "ContractedCost",
	"EffectiveCost", "InvoiceIssuerName", "ListCost", "PricingQuantity",
	"PricingUnit", "ProviderName", "PublisherName", "ServiceCategory", "ServiceName",
}

// specColumns is every column FOCUS 1.2 defines. Any other column must
// carry the x_ prefix (attributes/column_handling.md).
var specColumns = append(slices.Clone(specMandatory),
	"AvailabilityZone", "BillingAccountType", "CapacityReservationId", "CapacityReservationStatus",
	"ChargeFrequency", "CommitmentDiscountCategory", "CommitmentDiscountId", "CommitmentDiscountName",
	"CommitmentDiscountQuantity", "CommitmentDiscountStatus", "CommitmentDiscountType",
	"CommitmentDiscountUnit", "ConsumedQuantity", "ConsumedUnit", "ContractedUnitPrice", "InvoiceId",
	"ListUnitPrice", "PricingCategory", "PricingCurrency", "PricingCurrencyContractedUnitPrice",
	"PricingCurrencyEffectiveCost", "PricingCurrencyListUnitPrice", "RegionId", "RegionName",
	"ResourceId", "ResourceName", "ResourceType", "ServiceSubcategory", "SkuId", "SkuMeter",
	"SkuPriceDetails", "SkuPriceId", "SubAccountId", "SubAccountName", "SubAccountType", "Tags",
)

// notNullable are the columns whose spec says "MUST NOT be null", among
// the ones the export carries.
var notNullable = []string{
	"BilledCost", "BillingAccountId", "BillingCurrency", "BillingPeriodEnd", "BillingPeriodStart",
	"ChargeCategory", "ChargePeriodEnd", "ChargePeriodStart", "ContractedCost", "EffectiveCost",
	"InvoiceIssuerName", "ListCost", "ProviderName", "PublisherName", "ServiceCategory",
	"ServiceName", "ChargeFrequency", "ServiceSubcategory",
}

var (
	chargeCategories = []string{"Usage", "Purchase", "Tax", "Credit", "Adjustment"}
	chargeFrequency  = []string{"One-Time", "Recurring", "Usage-Based"}
	// serviceCategories is the allowed list of columns/servicecategory.md.
	serviceCategories = []string{
		"AI and Machine Learning", "Analytics", "Business Applications", "Compute", "Databases",
		"Developer Tools", "Multicloud", "Identity", "Integration", "Internet of Things",
		"Management and Governance", "Media", "Migration", "Mobile", "Networking", "Security",
		"Storage", "Web", "Other",
	}
	// aiSubcategories are columns/servicesubcategory.md's children of
	// "AI and Machine Learning".
	aiSubcategories = []string{
		"AI Platforms", "Bots", "Generative AI", "Machine Learning",
		"Natural Language Processing", "Other (AI and Machine Learning)",
	}
	// dateTime is attributes/datetime_format.md: extended ISO 8601 in UTC.
	dateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	// numeric is attributes/numeric_format.md: an integer, a decimal or E
	// notation; no sign on positives, no separators, no units.
	numeric = regexp.MustCompile(`^-?\d+(\.\d+)?([eE]-?\d+)?$`)
	// currency is ISO 4217 (attributes/currency_format.md).
	currency = regexp.MustCompile(`^[A-Z]{3}$`)
	// unit is attributes/unit_format.md: plural units, optionally led by
	// a whole-number quantity ("1000000 Tokens").
	unit = regexp.MustCompile(`^(\d+ )?[A-Z][A-Za-z]*s$`)
)

// validate checks a FOCUS CSV against the 1.2 rules the export is subject
// to and returns every violation.
func validate(t *testing.T, data []byte) []string {
	t.Helper()
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	if len(records) == 0 {
		return []string{"no header"}
	}
	header := records[0]
	col := map[string]int{}
	var bad []string
	for i, name := range header {
		col[name] = i
		if !slices.Contains(specColumns, name) && !regexp.MustCompile(`^x_[A-Z][A-Za-z0-9]*$`).MatchString(name) {
			bad = append(bad, fmt.Sprintf("column %q is neither a FOCUS 1.2 column nor x_-prefixed", name))
		}
	}
	for _, name := range specMandatory {
		if _, ok := col[name]; !ok {
			bad = append(bad, "mandatory column missing: "+name)
		}
	}
	if len(bad) > 0 {
		return bad
	}
	for n, rec := range records[1:] {
		get := func(name string) string { return rec[col[name]] }
		fail := func(format string, args ...any) {
			bad = append(bad, fmt.Sprintf("row %d: ", n+1)+fmt.Sprintf(format, args...))
		}
		for _, name := range notNullable {
			if _, ok := col[name]; ok && get(name) == "" {
				fail("%s must not be null", name)
			}
		}
		category := get("ChargeCategory")
		if !slices.Contains(chargeCategories, category) {
			fail("ChargeCategory %q is not an allowed value", category)
		}
		if c := get("ChargeClass"); c != "" && c != "Correction" {
			fail("ChargeClass %q must be null or Correction", c)
		}
		if f := get("ChargeFrequency"); !slices.Contains(chargeFrequency, f) {
			fail("ChargeFrequency %q is not an allowed value", f)
		} else if category == "Purchase" && f == "Usage-Based" {
			fail("ChargeFrequency must not be Usage-Based for a Purchase")
		}
		if s := get("ServiceCategory"); !slices.Contains(serviceCategories, s) {
			fail("ServiceCategory %q is not an allowed value", s)
		}
		if s := get("ServiceSubcategory"); get("ServiceCategory") == "AI and Machine Learning" && !slices.Contains(aiSubcategories, s) {
			fail("ServiceSubcategory %q is not a child of AI and Machine Learning", s)
		}
		for _, name := range []string{"BillingPeriodStart", "BillingPeriodEnd", "ChargePeriodStart", "ChargePeriodEnd"} {
			if !dateTime.MatchString(get(name)) {
				fail("%s %q is not an ISO 8601 UTC date/time", name, get(name))
			}
		}
		if get("ChargePeriodStart") >= get("ChargePeriodEnd") {
			fail("the charge period ends before it starts")
		}
		if get("ChargePeriodStart") < get("BillingPeriodStart") || get("ChargePeriodEnd") > get("BillingPeriodEnd") {
			fail("the charge period %s–%s is outside its billing period %s–%s",
				get("ChargePeriodStart"), get("ChargePeriodEnd"), get("BillingPeriodStart"), get("BillingPeriodEnd"))
		}
		for _, name := range []string{"BilledCost", "ContractedCost", "EffectiveCost", "ListCost", "PricingQuantity", "ConsumedQuantity"} {
			if v := get(name); v != "" && !numeric.MatchString(v) {
				fail("%s %q is not a FOCUS numeric value", name, v)
			}
		}
		if !currency.MatchString(get("BillingCurrency")) {
			fail("BillingCurrency %q is not an ISO 4217 code", get("BillingCurrency"))
		}
		// PricingQuantity and PricingUnit: not null for Usage and Purchase
		// (not corrections); the unit is null exactly when the quantity is.
		if (category == "Usage" || category == "Purchase") && get("ChargeClass") == "" && get("PricingQuantity") == "" {
			fail("PricingQuantity must not be null for a %s", category)
		}
		if (get("PricingQuantity") == "") != (get("PricingUnit") == "") {
			fail("PricingUnit must be null exactly when PricingQuantity is")
		}
		if u := get("PricingUnit"); u != "" && !unit.MatchString(u) {
			fail("PricingUnit %q does not follow the unit format", u)
		}
		// ConsumedQuantity: null unless Usage; not null for Usage (not a
		// correction); ConsumedUnit null exactly when it is.
		consumed := get("ConsumedQuantity")
		if category != "Usage" && consumed != "" {
			fail("ConsumedQuantity must be null for a %s", category)
		}
		if category == "Usage" && get("ChargeClass") == "" && consumed == "" {
			fail("ConsumedQuantity must not be null for Usage")
		}
		if (consumed == "") != (get("ConsumedUnit") == "") {
			fail("ConsumedUnit must be null exactly when ConsumedQuantity is")
		}
		if u := get("ConsumedUnit"); u != "" && !unit.MatchString(u) {
			fail("ConsumedUnit %q does not follow the unit format", u)
		}
		// SkuId, when carried: not null for Usage and Purchase.
		if _, ok := col["SkuId"]; ok && (category == "Usage" || category == "Purchase") && get("ChargeClass") == "" && get("SkuId") == "" {
			fail("SkuId must not be null for a %s", category)
		}
		// Tags: null, or a JSON object of scalar values (key_value_format.md).
		if tags := get("Tags"); tags != "" {
			var m map[string]any
			if err := json.Unmarshal([]byte(tags), &m); err != nil {
				fail("Tags %q is not a JSON object", tags)
			}
			for k, v := range m {
				switch v.(type) {
				case map[string]any, []any:
					fail("Tags value for %q must be a scalar", k)
				}
			}
		}
		// The FOCUS cost relations this export relies on: costs are never
		// negative here, and a plan's included usage is billed nothing.
		for _, name := range []string{"BilledCost", "EffectiveCost", "ContractedCost", "ListCost"} {
			if v, err := strconv.ParseFloat(get(name), 64); err != nil || v < 0 {
				fail("%s %q must be a non-negative decimal", name, get(name))
			}
		}
	}
	return bad
}

func sampleDataset() ([]focus.Usage, []focus.Purchase) {
	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	maxPlan := focus.Plan{ID: "claude-max-5x", Display: "Claude Max 5x"}
	usage := []focus.Usage{
		{Day: day, Provider: "anthropic", Model: "claude-opus-5", Plan: maxPlan, PlanCovered: true,
			Requests: 40, Tokens: 3_250_000, ListUSD: 48.75},
		{Day: day, Provider: "anthropic", Model: "claude-opus-5", Requests: 2, Tokens: 1_100,
			BilledUSD: 1.25, ListUSD: 1.25},
		{Day: day.Add(24 * time.Hour), Provider: "openai", Model: "gpt-5.5", Requests: 3, Tokens: 12_000,
			BilledUSD: 0.06, ListUSD: 0.06},
		{Day: day.Add(24 * time.Hour), Provider: "anthropic", Model: "", PlanCovered: true,
			Requests: 1, Tokens: 10, Unpriced: true},
	}
	purchases := []focus.Purchase{
		// Spans a month boundary, so it becomes two charges.
		{Provider: "anthropic", Plan: maxPlan, From: day, To: day.Add(48 * time.Hour),
			Amount: 6.57, Currency: "USD", Months: 0.0657, ListPriced: true},
		{Provider: "openai", Plan: focus.Plan{ID: "gpt-plus", Display: "ChatGPT Plus"}, From: day, To: day.Add(24 * time.Hour),
			Amount: 0.6, Currency: "EUR", Months: 0.0329},
	}
	return usage, purchases
}

func TestExportConformsToFOCUS12(t *testing.T) {
	usage, purchases := sampleDataset()
	var buf bytes.Buffer
	if err := focus.WriteCSV(&buf, focus.Build(usage, purchases)); err != nil {
		t.Fatal(err)
	}
	for _, v := range validate(t, buf.Bytes()) {
		t.Error(v)
	}
}

// An empty window is still a dataset: the header names every column.
func TestEmptyExportStillHasTheMandatoryHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := focus.WriteCSV(&buf, nil); err != nil {
		t.Fatal(err)
	}
	for _, v := range validate(t, buf.Bytes()) {
		t.Error(v)
	}
}

// The validator itself catches what it claims to: a dataset that breaks
// the rules must fail it.
func TestTheConformanceCheckRejectsViolations(t *testing.T) {
	cases := map[string]string{
		"missing mandatory": "BilledCost\n1\n",
		"bad category":      headerOf(t) + "\n" + rowWith(t, map[string]string{"ChargeCategory": "Subscription"}),
		"bad date":          headerOf(t) + "\n" + rowWith(t, map[string]string{"ChargePeriodStart": "2026-09-30"}),
		"signed number":     headerOf(t) + "\n" + rowWith(t, map[string]string{"BilledCost": "+1"}),
		"purchase consumes": headerOf(t) + "\n" + rowWith(t, map[string]string{"ChargeCategory": "Purchase", "ChargeFrequency": "Recurring"}),
		"unknown column":    headerOf(t) + ",Model\n" + rowWith(t, nil) + ",x",
	}
	for name, data := range cases {
		if len(validate(t, []byte(data))) == 0 {
			t.Errorf("%s: the check accepted a non-conforming dataset", name)
		}
	}
}

// headerOf and rowWith build a one-row export and change some of its
// cells.
func headerOf(t *testing.T) string {
	t.Helper()
	return joinCSV(t, focus.Columns)
}

func rowWith(t *testing.T, change map[string]string) string {
	t.Helper()
	usage, _ := sampleDataset()
	rec := focus.Build(usage[2:3], nil)[0].Record()
	for i, name := range focus.Columns {
		if v, ok := change[name]; ok {
			rec[i] = v
		}
	}
	return joinCSV(t, rec)
}

func joinCSV(t *testing.T, rec []string) string {
	t.Helper()
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.Write(rec); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	return string(bytes.TrimRight(b.Bytes(), "\n"))
}
