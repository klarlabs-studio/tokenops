package focus_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/focus"
)

// The rules below are transcribed from the FOCUS 1.4 specification
// (github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec, tag v1.4,
// specification/datasets/cost_and_usage/columns/*.md and
// specification/attributes/*.md), not from the package under test.

// spec14Mandatory are the Cost and Usage columns FOCUS 1.4 marks
// Mandatory.
var spec14Mandatory = []string{
	"BilledCost", "BillingAccountId", "BillingAccountName", "BillingCurrency",
	"BillingPeriodEnd", "BillingPeriodStart", "ChargeCategory", "ChargeClass",
	"ChargeDescription", "ChargePeriodEnd", "ChargePeriodStart", "ContractedCost",
	"EffectiveCost", "HostProviderName", "InvoiceIssuerName", "ListCost", "PricingQuantity",
	"PricingUnit", "ServiceCategory", "ServiceName", "ServiceProviderName",
}

// spec14Columns is every Cost and Usage column FOCUS 1.4 defines. 1.2's
// ProviderName and PublisherName are not among them.
var spec14Columns = append(slices.Clone(spec14Mandatory),
	"AllocatedMethodDetails", "AllocatedMethodId", "AllocatedResourceId", "AllocatedResourceName",
	"AllocatedTags", "AvailabilityZone", "BillingAccountType", "CapacityReservationId",
	"CapacityReservationStatus", "ChargeFrequency", "CommitmentDiscountCategory", "CommitmentDiscountId",
	"CommitmentDiscountName", "CommitmentDiscountQuantity", "CommitmentDiscountStatus",
	"CommitmentDiscountType", "CommitmentDiscountUnit", "CommitmentProgramEligibilityDetails",
	"ConsumedQuantity", "ConsumedUnit", "ContractApplied", "ContractedUnitPrice", "InvoiceDetailId",
	"InvoiceId", "ListUnitPrice", "PricingCategory", "PricingCurrency",
	"PricingCurrencyContractedUnitPrice", "PricingCurrencyEffectiveCost", "PricingCurrencyListUnitPrice",
	"RegionId", "RegionName", "ResourceId", "ResourceName", "ResourceType", "ServiceSubcategory",
	"SkuId", "SkuMeter", "SkuPriceDetails", "SkuPriceId", "SubAccountId", "SubAccountName",
	"SubAccountType", "Tags",
)

// customColumn is attributes/custom_column_handling.md: x_ then Pascal
// case.
var customColumn = regexp.MustCompile(`^x_[A-Z][A-Za-z0-9]*$`)

// validate14 checks a FOCUS CSV against the 1.4 rules the export is
// subject to, and against what TokenOps owes as a data generator that
// issues no invoice, and returns every violation.
func validate14(t *testing.T, data []byte) []string {
	t.Helper()
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	if len(records) == 0 {
		return []string{"no header"}
	}
	col := map[string]int{}
	var bad []string
	for i, name := range records[0] {
		col[name] = i
		if !slices.Contains(spec14Columns, name) && !customColumn.MatchString(name) {
			bad = append(bad, fmt.Sprintf("column %q is neither a FOCUS 1.4 column nor x_-prefixed", name))
		}
	}
	for _, name := range spec14Mandatory {
		if _, ok := col[name]; !ok {
			bad = append(bad, "mandatory column missing: "+name)
		}
	}
	if len(bad) > 0 {
		return bad
	}
	for n, rec := range records[1:] {
		get := func(name string) string {
			if i, ok := col[name]; ok {
				return rec[i]
			}
			return ""
		}
		num := func(name string) float64 {
			v, _ := strconv.ParseFloat(get(name), 64)
			return v
		}
		fail := func(format string, args ...any) {
			bad = append(bad, fmt.Sprintf("row %d: ", n+1)+fmt.Sprintf(format, args...))
		}
		category := get("ChargeCategory")
		usageOrPurchase := (category == "Usage" || category == "Purchase") && get("ChargeClass") == ""
		for _, name := range []string{"BilledCost", "BillingAccountId", "BillingCurrency", "BillingPeriodEnd",
			"BillingPeriodStart", "ChargeCategory", "ChargePeriodEnd", "ChargePeriodStart", "ContractedCost",
			"EffectiveCost", "InvoiceIssuerName", "ListCost", "ServiceCategory", "ServiceName",
			"ServiceProviderName"} {
			if get(name) == "" {
				fail("%s must not be null", name)
			}
		}
		if !slices.Contains(chargeCategories, category) {
			fail("ChargeCategory %q is not an allowed value", category)
		}
		if f := get("ChargeFrequency"); f != "" && !slices.Contains(chargeFrequency, f) {
			fail("ChargeFrequency %q is not an allowed value", f)
		} else if category == "Purchase" && f == "Usage-Based" {
			fail("ChargeFrequency must not be Usage-Based for a Purchase")
		}
		if s := get("ServiceSubcategory"); get("ServiceCategory") == "AI and Machine Learning" && s != "" && !slices.Contains(aiSubcategories, s) {
			fail("ServiceSubcategory %q is not a child of AI and Machine Learning", s)
		}
		for _, name := range []string{"BillingPeriodStart", "BillingPeriodEnd", "ChargePeriodStart", "ChargePeriodEnd"} {
			if !dateTime.MatchString(get(name)) {
				fail("%s %q is not an ISO 8601 UTC date/time", name, get(name))
			}
		}
		for _, name := range []string{"BilledCost", "ContractedCost", "ContractedUnitPrice", "EffectiveCost",
			"ListCost", "ListUnitPrice", "PricingQuantity", "ConsumedQuantity", focus.ColumnEstimatedCost} {
			if v := get(name); v != "" && !numeric.MatchString(v) {
				fail("%s %q is not a FOCUS numeric value", name, v)
			}
		}
		if !currency.MatchString(get("BillingCurrency")) {
			fail("BillingCurrency %q is not an ISO 4217 code", get("BillingCurrency"))
		}
		// HostProviderName: not null for a service deployed on
		// infrastructure; it matches ServiceProviderName unless a host was
		// chosen or exposed.
		if get("HostProviderName") == "" {
			fail("HostProviderName must not be null for a model served by its vendor")
		}
		// SkuId, SkuPriceId, PricingQuantity, ListUnitPrice and
		// ContractedUnitPrice: not null for Usage and Purchase (not
		// corrections); each price and the quantity is null exactly when
		// SkuPriceId is.
		skuPrice := get("SkuPriceId")
		if usageOrPurchase {
			for _, name := range []string{"SkuId", "SkuPriceId", "PricingQuantity", "ListUnitPrice", "ContractedUnitPrice"} {
				if get(name) == "" {
					fail("%s must not be null for a %s", name, category)
				}
			}
		}
		for _, name := range []string{"PricingQuantity", "ListUnitPrice", "ContractedUnitPrice", "ConsumedQuantity"} {
			if skuPrice == "" && get(name) != "" {
				fail("%s must be null when SkuPriceId is", name)
			}
		}
		if (get("PricingQuantity") == "") != (get("PricingUnit") == "") {
			fail("PricingUnit must be null exactly when PricingQuantity is")
		}
		if u := get("PricingUnit"); u != "" && !unit.MatchString(u) {
			fail("PricingUnit %q does not follow the unit format", u)
		}
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
		// A cost metric is its unit price times PricingQuantity, when both
		// are present (pricingquantity.md, listcost.md, contractedcost.md).
		for cost, price := range map[string]string{"ListCost": "ListUnitPrice", "ContractedCost": "ContractedUnitPrice"} {
			if get(price) == "" || get("PricingQuantity") == "" {
				continue
			}
			if want := num(price) * num("PricingQuantity"); math.Abs(num(cost)-want) > 1e-9 {
				fail("%s %s is not %s × PricingQuantity = %v", cost, get(cost), price, want)
			}
		}
		for _, name := range []string{"ListUnitPrice", "ContractedUnitPrice"} {
			if get(name) != "" && num(name) < 0 {
				fail("%s must be non-negative", name)
			}
		}
		// BilledCost: as invoiced, never estimated, and 0 on rows from an
		// entity that issues no invoice. TokenOps issues none, so every
		// BilledCost is 0, and EffectiveCost with it.
		if num("BilledCost") != 0 || num("EffectiveCost") != 0 {
			fail("BilledCost %s and EffectiveCost %s must be 0: TokenOps issues no invoice", get("BilledCost"), get("EffectiveCost"))
		}
	}
	return bad
}

func TestExportConformsToFOCUS14(t *testing.T) {
	usage, purchases := sampleDataset14()
	var buf bytes.Buffer
	if err := focus.V1_4.WriteCSV(&buf, focus.V1_4.Build(usage, purchases)); err != nil {
		t.Fatal(err)
	}
	for _, v := range validate14(t, buf.Bytes()) {
		t.Error(v)
	}
}

func TestEmpty14ExportStillHasTheMandatoryHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := focus.V1_4.WriteCSV(&buf, nil); err != nil {
		t.Fatal(err)
	}
	for _, v := range validate14(t, buf.Bytes()) {
		t.Error(v)
	}
}

// The 1.4 validator catches what it claims to.
func TestThe14ConformanceCheckRejectsViolations(t *testing.T) {
	header := joinCSV(t, focus.V1_4.Columns)
	row := func(change map[string]string) string {
		usage, _ := sampleDataset14()
		rec := focus.V1_4.Record(focus.V1_4.Build(usage[1:2], nil)[0])
		for i, name := range focus.V1_4.Columns {
			if v, ok := change[name]; ok {
				rec[i] = v
			}
		}
		return joinCSV(t, rec)
	}
	cases := map[string]string{
		"1.2 provider column": joinCSV(t, focus.V1_2.Columns) + "\n",
		"estimate billed":     header + "\n" + row(map[string]string{"BilledCost": "1.25"}),
		"no sku price":        header + "\n" + row(map[string]string{"SkuPriceId": ""}),
		"list not a product":  header + "\n" + row(map[string]string{"ListCost": "99"}),
		"no host":             header + "\n" + row(map[string]string{"HostProviderName": ""}),
	}
	for name, data := range cases {
		if len(validate14(t, []byte(data))) == 0 {
			t.Errorf("%s: the check accepted a non-conforming dataset", name)
		}
	}
}

// sampleDataset14 is sampleDataset with each priced charge split by kind
// of token at its rate.
func sampleDataset14() ([]focus.Usage, []focus.Purchase) {
	usage, purchases := sampleDataset()
	opus := func(in, read, write, out int64) []focus.TokenPrice {
		return []focus.TokenPrice{
			{Kind: focus.TokensInput, Tokens: in, PerMillion: 5},
			{Kind: focus.TokensCacheRead, Tokens: read, PerMillion: 0.5},
			{Kind: focus.TokensCacheWrite, Tokens: write, PerMillion: 6.25},
			{Kind: focus.TokensCacheWrite1h, Tokens: 0, PerMillion: 10},
			{Kind: focus.TokensOutput, Tokens: out, PerMillion: 25},
		}
	}
	usage[0].Priced = opus(250_000, 2_500_000, 300_000, 200_000)
	usage[1].Priced = opus(800, 0, 0, 300)
	usage[2].Priced = opus(600, 0, 0, 300)
	usage[3].Priced = []focus.TokenPrice{
		{Kind: focus.TokensInput, Tokens: 10_000, PerMillion: 2.5},
		{Kind: focus.TokensOutput, Tokens: 2_000, PerMillion: 10},
	}
	// usage[4] has no rate: it stays one row.
	return usage, purchases
}
