package focus_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/focus"
)

// The rules below are transcribed from the FOCUS 1.5 candidate
// recommendation (github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec,
// tag v1.5-cr, specification/datasets/cost_and_usage/dataset.md,
// specification/datasets/cost_and_usage/columns/*.md and
// specification/attributes/*.md), not from the package under test.

// spec15Columns is every Cost and Usage column FOCUS 1.5 defines: 1.4's,
// plus the requester and split-allocation columns. Its Mandatory columns
// are 1.4's.
var spec15Columns = append(slices.Clone(spec14Columns),
	"AllocatedServiceName", "CredentialId", "PrincipalId", "RequesterDetails")

// spec15UnitPricing are the columns dataset.md requires when the
// operating model includes unit pricing, as the export's does: it writes
// unit prices.
var spec15UnitPricing = []string{"ContractedUnitPrice", "ListUnitPrice", "SkuId", "SkuMeter", "SkuPriceDetails", "SkuPriceId"}

// skuPriceProperties are the FOCUS-defined SkuPriceDetails keys
// (skupricedetails.md); any other key must start with x_.
var skuPriceProperties = []string{
	"CoreCount", "DiskMaxIops", "DiskSpace", "DiskType", "GpuCount", "InstanceType", "InstanceSeries",
	"MemorySize", "ModelDeveloper", "ModelFamily", "ModelId", "ModelVersion", "NetworkMaxIops",
	"NetworkMaxThroughput", "OperatingSystem", "Redundancy", "StorageClass", "TokenCacheAction", "TokenDirection",
}

var (
	tokenDirections   = []string{"Input", "Output"}
	tokenCacheActions = []string{"Uncached", "Read", "Write", "Other"}
)

// planColumns are TokenOps' documented plan-window columns.
var planColumns = []string{
	focus.ColumnPlanWindowName, focus.ColumnPlanWindowCount, focus.ColumnPlanUtilization,
	focus.ColumnPlanCoverage, focus.ColumnPlanLapsedValue,
}

// validate15 checks a FOCUS CSV against the 1.5 rules the export is
// subject to, the 1.4 ones it keeps among them, and TokenOps' own rules
// for its custom columns, and returns every violation.
func validate15(t *testing.T, data []byte) []string {
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
		if !slices.Contains(spec15Columns, name) && !customColumn.MatchString(name) {
			bad = append(bad, fmt.Sprintf("column %q is neither a FOCUS 1.5 column nor x_-prefixed", name))
		}
		// custom_column_handling.md: at most 50 characters.
		if strings.HasPrefix(name, "x_") && len(name) > 50 {
			bad = append(bad, fmt.Sprintf("custom column %q is longer than 50 characters", name))
		}
		// TokenOps rule: no requester attribution, so no personal data.
		if slices.Contains([]string{"PrincipalId", "CredentialId", "RequesterDetails"}, name) {
			bad = append(bad, "requester column "+name+" must not be written")
		}
	}
	for _, name := range append(slices.Clone(spec14Mandatory), spec15UnitPricing...) {
		if _, ok := col[name]; !ok {
			bad = append(bad, "required column missing: "+name)
		}
	}
	if len(bad) > 0 {
		return bad
	}
	// The 1.4 row rules still hold; the header is checked above.
	rows14 := [][]string{columns14Of(records[0])}
	for _, rec := range records[1:] {
		rows14 = append(rows14, pick(rec, records[0], col))
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.WriteAll(rows14)
	bad = append(bad, validate14(t, buf.Bytes())...)

	type skuKey struct{ provider, id string }
	details := map[skuKey]string{}
	meters := map[skuKey]string{}
	priceUnits := map[skuKey]string{}
	priceCategory := map[skuKey]string{}
	priceSku := map[skuKey]string{}
	listPrice := map[string]string{}
	contractedPrice := map[string]string{}
	for n, rec := range records[1:] {
		get := func(name string) string { return rec[col[name]] }
		num := func(name string) float64 {
			v, _ := strconv.ParseFloat(get(name), 64)
			return v
		}
		fail := func(format string, args ...any) {
			bad = append(bad, fmt.Sprintf("row %d: ", n+1)+fmt.Sprintf(format, args...))
		}
		category, provider := get("ChargeCategory"), get("ServiceProviderName")
		usageOrPurchase := (category == "Usage" || category == "Purchase") && get("ChargeClass") == ""

		// contractedunitprice.md / contractedcost.md: equal to the list
		// price and cost when no negotiated pricing terms apply, as none
		// do to anything TokenOps writes.
		if usageOrPurchase {
			if get("ContractedUnitPrice") != get("ListUnitPrice") {
				fail("ContractedUnitPrice %s must equal ListUnitPrice %s: no negotiated terms apply",
					get("ContractedUnitPrice"), get("ListUnitPrice"))
			}
			if math.Abs(num("ContractedCost")-num("ListCost")) > 1e-9 {
				fail("ContractedCost %s must equal ListCost %s: no negotiated terms apply", get("ContractedCost"), get("ListCost"))
			}
		}

		// skumeter.md: null when SkuId is; should not be null otherwise;
		// consistent for a provider and SKU.
		sku, meter := get("SkuId"), get("SkuMeter")
		if sku == "" && meter != "" {
			fail("SkuMeter must be null when SkuId is")
		}
		if sku != "" && meter == "" {
			fail("SkuMeter should not be null when SkuId is not")
		}
		if sku != "" {
			k := skuKey{provider, sku}
			if m, ok := meters[k]; ok && m != meter {
				fail("SkuId %s has SkuMeter %q and %q", sku, m, meter)
			}
			meters[k] = meter
		}

		// skupriceid.md: for a provider, one parent SkuId, one PricingUnit,
		// never both Usage and Purchase, one list price per currency and
		// charge period start, one contracted price per account too.
		priceID := get("SkuPriceId")
		if priceID != "" {
			k := skuKey{provider, priceID}
			for _, c := range []struct {
				seen  map[skuKey]string
				value string
				what  string
			}{{priceUnits, get("PricingUnit"), "PricingUnit"}, {priceCategory, category, "ChargeCategory"}, {priceSku, sku, "SkuId"}} {
				if v, ok := c.seen[k]; ok && v != c.value {
					fail("SkuPriceId %s has %s %q and %q", priceID, c.what, v, c.value)
				}
				c.seen[k] = c.value
			}
			lk := strings.Join([]string{provider, priceID, get("BillingCurrency"), get("ChargePeriodStart")}, "|")
			if v, ok := listPrice[lk]; ok && v != get("ListUnitPrice") {
				fail("SkuPriceId %s has two list prices for one currency and charge period start", priceID)
			}
			listPrice[lk] = get("ListUnitPrice")
			ck := lk + "|" + get("BillingAccountId")
			if v, ok := contractedPrice[ck]; ok && v != get("ContractedUnitPrice") {
				fail("SkuPriceId %s has two contracted prices for one account, currency and charge period start", priceID)
			}
			contractedPrice[ck] = get("ContractedUnitPrice")
		}

		// skupricedetails.md.
		raw := get("SkuPriceDetails")
		if priceID == "" && raw != "" {
			fail("SkuPriceDetails must be null when SkuPriceId is")
		}
		if raw != "" {
			var d map[string]any
			if err := json.Unmarshal([]byte(raw), &d); err != nil {
				fail("SkuPriceDetails %q is not a JSON object", raw)
				continue
			}
			for key := range d {
				if !slices.Contains(skuPriceProperties, key) && !strings.HasPrefix(key, "x_") {
					fail("SkuPriceDetails key %q is neither FOCUS-defined nor x_-prefixed", key)
				}
			}
			direction, hasDirection := d["TokenDirection"]
			if hasDirection && !slices.Contains(tokenDirections, fmt.Sprint(direction)) {
				fail("TokenDirection %v is not an allowed value", direction)
			}
			if action, ok := d["TokenCacheAction"]; ok {
				if !slices.Contains(tokenCacheActions, fmt.Sprint(action)) {
					fail("TokenCacheAction %v is not an allowed value", action)
				}
				// Only tokens consumed from a request interact with a cache.
				if direction != "Input" {
					fail("TokenCacheAction is only for input tokens, not %v", direction)
				}
			}
			k := skuKey{provider, priceID}
			if v, ok := details[k]; ok && v != raw {
				fail("SkuPriceId %s has two SkuPriceDetails: %s and %s", priceID, v, raw)
			}
			details[k] = raw
		}

		// TokenOps' plan-window columns: on Purchase rows only, ratios
		// from 0 to 1, the window named exactly when utilisation is
		// measured, and the lapsed value the unused share of the fee.
		if category != "Purchase" {
			for _, name := range planColumns {
				if get(name) != "" {
					fail("%s must be null on a %s row", name, category)
				}
			}
			continue
		}
		for _, name := range []string{focus.ColumnPlanUtilization, focus.ColumnPlanCoverage} {
			if v := get(name); v != "" && (!numeric.MatchString(v) || num(name) < 0 || num(name) > 1) {
				fail("%s %q must be a fraction from 0 to 1", name, v)
			}
		}
		measured := get(focus.ColumnPlanUtilization) != ""
		for _, name := range []string{focus.ColumnPlanWindowName, focus.ColumnPlanWindowCount, focus.ColumnPlanLapsedValue} {
			if (get(name) != "") != measured {
				fail("%s must be null exactly when %s is", name, focus.ColumnPlanUtilization)
			}
		}
		if measured {
			want := num(focus.ColumnEstimatedCost) * (1 - num(focus.ColumnPlanUtilization))
			if math.Abs(num(focus.ColumnPlanLapsedValue)-want) > 1e-9 {
				fail("%s %s is not %s × (1 − %s) = %v", focus.ColumnPlanLapsedValue, get(focus.ColumnPlanLapsedValue),
					focus.ColumnEstimatedCost, focus.ColumnPlanUtilization, want)
			}
		}
	}
	return bad
}

// columns14Of and pick reduce a 1.5 record to the columns 1.4 knows, plus
// the custom ones, for the 1.4 row rules.
func columns14Of(header []string) []string {
	var out []string
	for _, name := range header {
		if slices.Contains(spec14Columns, name) || customColumn.MatchString(name) {
			out = append(out, name)
		}
	}
	return out
}

func pick(rec, header []string, col map[string]int) []string {
	names := columns14Of(header)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, rec[col[name]])
	}
	return out
}

// sampleDataset15 is sampleDataset14 with the plans' window use measured
// on some of their charge periods.
func sampleDataset15() ([]focus.Usage, []focus.Purchase) {
	usage, purchases := sampleDataset14()
	util, cover := 0.625, 0.973
	periods := purchases[0].ChargePeriods()
	purchases[0].Use = []focus.PlanUse{
		{Start: periods[0].From, Window: "weekly", Windows: 1, Utilization: &util, Coverage: &cover},
		// Coverage alone: no window of the plan reset in the period.
		{Start: periods[1].From, Coverage: &cover},
	}
	return usage, purchases
}

func TestExportConformsToFOCUS15(t *testing.T) {
	usage, purchases := sampleDataset15()
	var buf bytes.Buffer
	if err := focus.V1_5.WriteCSV(&buf, focus.V1_5.Build(usage, purchases)); err != nil {
		t.Fatal(err)
	}
	for _, v := range validate15(t, buf.Bytes()) {
		t.Error(v)
	}
}

func TestEmpty15ExportStillHasTheRequiredHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := focus.V1_5.WriteCSV(&buf, nil); err != nil {
		t.Fatal(err)
	}
	for _, v := range validate15(t, buf.Bytes()) {
		t.Error(v)
	}
}

// consumedquantity.md: a token counted on a cache Read or Write row is not
// counted again on a row of the same usage that carries neither, so a
// charge's rows add up to its tokens.
func TestFOCUS15CountsEachTokenOnce(t *testing.T) {
	usage, _ := sampleDataset15()
	u := usage[0]
	var sum int64
	for _, r := range focus.V1_5.Build([]focus.Usage{u}, nil) {
		sum += *r.ConsumedQuantity
	}
	if sum != u.Tokens {
		t.Errorf("the charge's rows count %d tokens, the charge has %d", sum, u.Tokens)
	}
}

// The 1.5 validator catches what it claims to.
func TestThe15ConformanceCheckRejectsViolations(t *testing.T) {
	header := joinCSV(t, focus.V1_5.Columns)
	usage, purchases := sampleDataset15()
	covered := focus.V1_5.Build(usage[0:1], nil)
	purchase := focus.V1_5.Build(nil, purchases[0:1])[0]
	row := func(r focus.Row, change map[string]string) string {
		rec := focus.V1_5.Record(r)
		for i, name := range focus.V1_5.Columns {
			if v, ok := change[name]; ok {
				rec[i] = v
			}
		}
		return joinCSV(t, rec)
	}
	output := covered[len(covered)-1]
	cases := map[string]string{
		"1.4 header":             joinCSV(t, focus.V1_4.Columns) + "\n",
		"plan contracted at 0":   header + "\n" + row(covered[0], map[string]string{"ContractedUnitPrice": "0", "ContractedCost": "0"}),
		"unknown cache action":   header + "\n" + row(covered[0], map[string]string{"SkuPriceDetails": `{"TokenCacheAction":"Cached","TokenDirection":"Input"}`}),
		"cache action on output": header + "\n" + row(output, map[string]string{"SkuPriceDetails": `{"TokenCacheAction":"Uncached","TokenDirection":"Output"}`}),
		"unprefixed property":    header + "\n" + row(covered[0], map[string]string{"SkuPriceDetails": `{"CacheRetention":"5 Minutes"}`}),
		"details differ": header + "\n" + row(covered[0], nil) + "\n" +
			row(covered[0], map[string]string{"SkuPriceDetails": `{"ModelId":"other","TokenDirection":"Input"}`}),
		"usage and purchase": header + "\n" + row(covered[0], nil) + "\n" +
			row(purchase, map[string]string{"SkuPriceId": covered[0].SkuPriceID}),
		"no meter":                 header + "\n" + row(covered[0], map[string]string{"SkuMeter": ""}),
		"custom without x_":        header + ",PlanUtilization\n" + row(purchase, nil) + ",1",
		"lapsed value off":         header + "\n" + row(purchase, map[string]string{focus.ColumnPlanLapsedValue: "99"}),
		"utilisation over 1":       header + "\n" + row(purchase, map[string]string{focus.ColumnPlanUtilization: "62.5"}),
		"plan columns on usage":    header + "\n" + row(covered[0], map[string]string{focus.ColumnPlanCoverage: "0.5"}),
		"window without measuring": header + "\n" + row(purchase, map[string]string{focus.ColumnPlanUtilization: ""}),
		"requester column":         header + ",PrincipalId\n" + row(purchase, nil) + ",user-1",
	}
	for name, data := range cases {
		if len(validate15(t, []byte(data))) == 0 {
			t.Errorf("%s: the check accepted a non-conforming dataset", name)
		}
	}
}

// Each kind of token is its own SKU, named after the model and the kind,
// metered and described as FOCUS 1.5's AI examples do.
func TestFOCUS15DescribesEachKindOfToken(t *testing.T) {
	usage, _ := sampleDataset15()
	rows := focus.V1_5.Build(usage[0:1], nil)
	want := []struct{ sku, meter, details string }{
		{"claude-opus-5/input", "Input Tokens",
			`{"ModelDeveloper":"Anthropic","ModelId":"claude-opus-5","TokenCacheAction":"Uncached","TokenDirection":"Input"}`},
		{"claude-opus-5/cache-read", "Cache Read Input Tokens",
			`{"ModelDeveloper":"Anthropic","ModelId":"claude-opus-5","TokenCacheAction":"Read","TokenDirection":"Input"}`},
		{"claude-opus-5/cache-write-5m", "Cache Write Input Tokens",
			`{"ModelDeveloper":"Anthropic","ModelId":"claude-opus-5","TokenCacheAction":"Write","TokenDirection":"Input","x_CacheRetention":"5 Minutes"}`},
		{"claude-opus-5/output", "Output Tokens",
			`{"ModelDeveloper":"Anthropic","ModelId":"claude-opus-5","TokenDirection":"Output"}`},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i, w := range want {
		r := rows[i]
		if r.SkuID != w.sku || r.SkuPriceID != w.sku || r.SkuMeter != w.meter || r.SkuPriceDetails != w.details {
			t.Errorf("row %d: sku %q price %q meter %q details %s; want %+v", i, r.SkuID, r.SkuPriceID, r.SkuMeter, r.SkuPriceDetails, w)
		}
	}
	// Usage a plan covered is contracted at its list rate; what the plan
	// was worth is ListCost against the estimate, which is 0.
	out := rows[3]
	if *out.ContractedUnitPrice != 25 || out.ContractedCost != out.ListCost || out.EstimatedCost != 0 {
		t.Errorf("contracted %v (%v) against list %v, estimate %v", *out.ContractedUnitPrice, out.ContractedCost, out.ListCost, out.EstimatedCost)
	}
	if strings.Contains(out.ChargeDescription, "ContractedCost is 0") {
		t.Errorf("description: %q", out.ChargeDescription)
	}
}

// A model without a rate card is one SKU for all its tokens: it names
// the model but neither a direction nor a cache action, which would
// describe only part of them. A model nobody named has no details.
func TestFOCUS15LumpedTokensHaveNoDirection(t *testing.T) {
	day := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	rows := focus.V1_5.Build([]focus.Usage{
		{Day: day, Provider: "openrouter", Model: "meta-llama/llama-5", Requests: 1, Tokens: 10, BilledUSD: 0.1},
		{Day: day, Provider: "openrouter", Model: "acme-7b", Requests: 1, Tokens: 10, BilledUSD: 0.1},
		{Day: day, Provider: "anthropic", Requests: 1, Tokens: 10, PlanCovered: true},
	}, nil)
	got := map[string]string{}
	for _, r := range rows {
		got[r.SkuID] = r.SkuPriceDetails + " " + r.SkuMeter
	}
	want := map[string]string{
		"meta-llama/llama-5/tokens": `{"ModelDeveloper":"Meta","ModelId":"meta-llama/llama-5"} Tokens`,
		"acme-7b/tokens":            `{"ModelId":"acme-7b"} Tokens`,
		"unidentified-model/tokens": ` Tokens`,
	}
	for sku, w := range want {
		if got[sku] != w {
			t.Errorf("%s: %q, want %q", sku, got[sku], w)
		}
	}
}

// A plan's Purchase rows carry how its windows were used in each charge
// period, and the part of the fee the unused window capacity paid for.
func TestFOCUS15PurchasesCarryPlanWindowUse(t *testing.T) {
	_, purchases := sampleDataset15()
	rows := focus.V1_5.Build(nil, purchases[0:1])
	if len(rows) != 2 {
		t.Fatalf("the plan spans two months: %+v", rows)
	}
	sep, oct := rows[0], rows[1]
	if sep.PlanWindowName != "weekly" || *sep.PlanWindowCount != 1 || *sep.PlanUtilization != 0.625 || *sep.PlanCoverage != 0.973 {
		t.Errorf("September: %q %v %v %v", sep.PlanWindowName, sep.PlanWindowCount, sep.PlanUtilization, sep.PlanCoverage)
	}
	if math.Abs(*sep.PlanLapsedValue-sep.EstimatedCost*0.375) > 1e-12 {
		t.Errorf("lapsed %v of fee %v", *sep.PlanLapsedValue, sep.EstimatedCost)
	}
	if oct.PlanUtilization != nil || oct.PlanLapsedValue != nil || oct.PlanWindowName != "" || *oct.PlanCoverage != 0.973 {
		t.Errorf("October measured coverage only: %+v", oct)
	}
	if sep.SkuMeter != "Subscription" || sep.SkuPriceDetails != "" {
		t.Errorf("meter %q, details %q", sep.SkuMeter, sep.SkuPriceDetails)
	}
}
