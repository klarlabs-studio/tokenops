// Package focus renders spend and usage as a FOCUS dataset: the FinOps
// Open Cost and Usage Specification that FinOps tools (Vantage, CloudZero,
// Cloudability and others) ingest. It writes FOCUS 1.4, the default,
// FOCUS 1.2 for tools that do not read 1.4 yet, and FOCUS 1.5 on request
// while 1.5 is a candidate recommendation. The conformance tests
// transcribe each version's column definitions and allowed values from
// the specification (github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec,
// tags v1.2, v1.4 and v1.5-cr).
//
// The FOCUS 1.2 mapping:
//
//   - A plan's fee is a Purchase, Recurring, prorated over the days it was
//     in force and split at calendar months, which stand in for billing
//     periods. A subscription is not a commitment discount, so its
//     EffectiveCost is what it costs.
//
//   - Usage a plan covered is a Usage row billed 0, with ListCost at the
//     API price: ListCost against EffectiveCost is the value the plan gave.
//
//   - Usage billed per token is a Usage row that costs what it cost.
//
//   - Tokens are ConsumedQuantity in "Tokens"; PricingQuantity counts
//     millions of tokens, the unit API prices are published in.
//
//   - Usage an allocation rule tagged is its own row, its tags in Tags.
//
// FOCUS 1.4 requires BilledCost to be what the invoice issuer invoiced,
// never an estimate, and 0 on rows from anyone who does not invoice, so
// that a dataset merged with the vendor's own does not count a charge
// twice. TokenOps invoices nothing, so its 1.4 rows carry:
//
//   - BilledCost and EffectiveCost 0.
//
//   - A row per kind of token (input, cache read, cache write, output),
//     each at its published rate: SkuPriceId names the model and the kind,
//     ListUnitPrice is the rate per million tokens and ListCost the rate
//     times PricingQuantity.
//
//   - ContractedUnitPrice at the list rate for usage billed per token, 0
//     for usage a plan covered: ListCost against ContractedCost is the
//     value the plan gave.
//
//   - x_EstimatedCost, what TokenOps estimates the charge cost: the
//     client-reported or rate-card cost of usage billed per token, 0 for
//     usage a plan covered, a plan's prorated fee for its purchase.
//
//   - ServiceProviderName and HostProviderName, which replace 1.2's
//     ProviderName and PublisherName, both the vendor.
//
// FOCUS 1.5 keeps the 1.4 rows and changes three things:
//
//   - A contracted price reflects only privately negotiated terms and
//     equals the list price when none apply. A plan is publicly priced, so
//     usage a plan covered is contracted at its list rate; what the plan
//     was worth is ListCost against x_EstimatedCost, which is 0 for it.
//
//   - Each kind of token is its own SKU (SkuId "<model>/<kind>"), with a
//     SkuMeter, and SkuPriceDetails names the model (ModelId, and
//     ModelDeveloper where it is known) and the tokens' TokenDirection and
//     TokenCacheAction. A cache write's retention is x_CacheRetention.
//
//   - A plan's Purchase rows carry how its windows were used in the
//     charge period, in custom columns: x_PlanWindowName and
//     x_PlanWindowCount (the longest window kind that reset in it, and how
//     many reset), x_PlanUtilization (the mean of those windows' peak
//     share used, 0 to 1), x_PlanCoverage (the share of the provider's
//     tokens a plan covered, 0 to 1) and x_PlanLapsedValue (the part of the
//     row's prorated fee that paid for window capacity that reset unused).
//     A subscription is still not a commitment discount: no standard
//     column changes for it.
//
// Only derived fields leave: providers, models, plans, counts, money and
// the operator's own allocation tags. No session title, path or prompt has
// a column to go in.
package focus

import (
	"cmp"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// Spec is a FOCUS version a dataset can be written in.
type Spec struct {
	// Version is the FOCUS version ("1.4").
	Version string
	// Columns are the dataset's columns, in the alphabetical order the
	// FOCUS examples use: every Mandatory column of the version, plus the
	// Recommended and Conditional ones TokenOps can fill truthfully.
	Columns  []string
	usage    func(Usage) []Row
	purchase func(Purchase) []Row
	record   func(Row) []string
	// planUse is true when the version carries plan-window use on its
	// Purchase rows.
	planUse bool
}

// V1_2, V1_4 and V1_5 are the versions the export writes.
var (
	V1_2 = Spec{Version: "1.2", Columns: columns12, usage: usageRows12, purchase: purchaseRows, record: Row.record12}
	V1_4 = Spec{Version: "1.4", Columns: columns14, usage: usageRows14, purchase: purchaseRows14, record: Row.record14}
	V1_5 = Spec{Version: "1.5", Columns: columns15, usage: usageRows15, purchase: purchaseRows15, record: Row.record15,
		planUse: true}
)

// CarriesPlanUse reports whether s writes plan-window use on a plan's
// Purchase rows, so a caller measures it only for a version that does.
func (s Spec) CarriesPlanUse() bool { return s.planUse }

// Latest is the version written when none is asked for.
var Latest = V1_4

// SpecFor returns the spec of version; empty is Latest.
func SpecFor(version string) (Spec, error) {
	switch strings.TrimSpace(version) {
	case "":
		return Latest, nil
	case V1_2.Version:
		return V1_2, nil
	case V1_4.Version:
		return V1_4, nil
	case V1_5.Version:
		return V1_5, nil
	}
	return Spec{}, fmt.Errorf("%w: %q; use %s, %s or %s", ErrUnsupportedVersion, version,
		V1_4.Version, V1_5.Version, V1_2.Version)
}

// ErrUnsupportedVersion refuses a FOCUS version the export cannot write.
var ErrUnsupportedVersion = errors.New("FOCUS version is not supported")

// columns12 are FOCUS 1.2's columns the export carries.
var columns12 = []string{
	"BilledCost",
	"BillingAccountId",
	"BillingAccountName",
	"BillingCurrency",
	"BillingPeriodEnd",
	"BillingPeriodStart",
	"ChargeCategory",
	"ChargeClass",
	"ChargeDescription",
	"ChargeFrequency",
	"ChargePeriodEnd",
	"ChargePeriodStart",
	"ConsumedQuantity",
	"ConsumedUnit",
	"ContractedCost",
	"EffectiveCost",
	"InvoiceIssuerName",
	"ListCost",
	"PricingQuantity",
	"PricingUnit",
	"ProviderName",
	"PublisherName",
	"ServiceCategory",
	"ServiceName",
	"ServiceSubcategory",
	"SkuId",
	"Tags",
}

// columns14 are FOCUS 1.4's columns the export carries, then its one
// custom column.
var columns14 = []string{
	"BilledCost",
	"BillingAccountId",
	"BillingAccountName",
	"BillingCurrency",
	"BillingPeriodEnd",
	"BillingPeriodStart",
	"ChargeCategory",
	"ChargeClass",
	"ChargeDescription",
	"ChargeFrequency",
	"ChargePeriodEnd",
	"ChargePeriodStart",
	"ConsumedQuantity",
	"ConsumedUnit",
	"ContractedCost",
	"ContractedUnitPrice",
	"EffectiveCost",
	"HostProviderName",
	"InvoiceIssuerName",
	"ListCost",
	"ListUnitPrice",
	"PricingQuantity",
	"PricingUnit",
	"ServiceCategory",
	"ServiceName",
	"ServiceProviderName",
	"ServiceSubcategory",
	"SkuId",
	"SkuPriceId",
	"Tags",
	ColumnEstimatedCost,
}

// columns15 are FOCUS 1.5's columns the export carries, then its custom
// columns. 1.5 requires SkuMeter and SkuPriceDetails wherever unit prices
// are written. Its requester columns (PrincipalId, CredentialId,
// RequesterDetails) are left out: TokenOps holds no identifier a provider
// publishes for a user or a key, and they would carry personal data.
var columns15 = []string{
	"BilledCost",
	"BillingAccountId",
	"BillingAccountName",
	"BillingCurrency",
	"BillingPeriodEnd",
	"BillingPeriodStart",
	"ChargeCategory",
	"ChargeClass",
	"ChargeDescription",
	"ChargeFrequency",
	"ChargePeriodEnd",
	"ChargePeriodStart",
	"ConsumedQuantity",
	"ConsumedUnit",
	"ContractedCost",
	"ContractedUnitPrice",
	"EffectiveCost",
	"HostProviderName",
	"InvoiceIssuerName",
	"ListCost",
	"ListUnitPrice",
	"PricingQuantity",
	"PricingUnit",
	"ServiceCategory",
	"ServiceName",
	"ServiceProviderName",
	"ServiceSubcategory",
	"SkuId",
	"SkuMeter",
	"SkuPriceDetails",
	"SkuPriceId",
	"Tags",
	ColumnEstimatedCost,
	ColumnPlanCoverage,
	ColumnPlanLapsedValue,
	ColumnPlanUtilization,
	ColumnPlanWindowCount,
	ColumnPlanWindowName,
}

// The custom columns FOCUS 1.5 rows carry on a plan's Purchase rows: how
// the plan's windows were used in the charge period. Each is null on
// usage rows and when nothing was measured.
const (
	// ColumnPlanWindowName is the plan window kind utilisation is
	// measured on: the longest kind that reset in the charge period.
	ColumnPlanWindowName = "x_PlanWindowName"
	// ColumnPlanWindowCount is how many windows of that kind reset in it.
	ColumnPlanWindowCount = "x_PlanWindowCount"
	// ColumnPlanUtilization is the mean of those windows' peak share
	// used, from 0 to 1.
	ColumnPlanUtilization = "x_PlanUtilization"
	// ColumnPlanCoverage is the share of the provider's tokens in the
	// charge period that a plan covered rather than billed per token,
	// from 0 to 1.
	ColumnPlanCoverage = "x_PlanCoverage"
	// ColumnPlanLapsedValue is the part of the row's prorated fee
	// (x_EstimatedCost) that paid for window capacity that reset unused:
	// x_EstimatedCost × (1 − x_PlanUtilization), in BillingCurrency.
	ColumnPlanLapsedValue = "x_PlanLapsedValue"
)

// ColumnEstimatedCost is the custom column holding what TokenOps
// estimates a charge cost, which FOCUS 1.4 keeps out of BilledCost.
const ColumnEstimatedCost = "x_EstimatedCost"

// Kinds of token, each priced at its own rate and written as its own
// FOCUS 1.4 row. The kind is the suffix of the row's SkuPriceId.
const (
	TokensInput        = "input"
	TokensCacheRead    = "cache-read"
	TokensCacheWrite   = "cache-write-5m"
	TokensCacheWrite1h = "cache-write-1h"
	TokensOutput       = "output"
)

// TokenPrice is one kind of token's share of a charge and its list rate.
type TokenPrice struct {
	Kind   string
	Tokens int64
	// PerMillion is the list price of a million tokens of the kind.
	PerMillion float64
}

// Allowed values used here, as FOCUS spells them.
const (
	CategoryUsage    = "Usage"
	CategoryPurchase = "Purchase"

	FrequencyUsageBased = "Usage-Based"
	FrequencyRecurring  = "Recurring"

	serviceCategory    = "AI and Machine Learning"
	serviceSubcategory = "Generative AI"

	unitTokens        = "Tokens"
	unitMillionTokens = "1000000 Tokens"
	unitMonths        = "Months"
)

// Plan names a subscription plan.
type Plan struct {
	// ID is the catalog name ("claude-max").
	ID string
	// Display is what the vendor calls it ("Claude Max"); empty when
	// the catalog does not know it.
	Display string
}

// Usage is one day's usage of one provider's model, accounted one way.
type Usage struct {
	// Day is the UTC day it happened on.
	Day             time.Time
	Provider, Model string
	// PlanCovered is true when a plan covered it; Plan is the plan in
	// force that day, zero when none is recorded.
	PlanCovered bool
	Plan        Plan
	Requests    int64
	Tokens      int64
	// BilledUSD is what was billed (0 when plan-covered); ListUSD is the
	// usage at API list prices.
	BilledUSD, ListUSD float64
	// Unpriced is true when some of the usage has no rate card, so ListUSD
	// understates it.
	Unpriced bool
	// Tags are the allocation tags the usage carries (project, client,
	// cost_center), nil when it carries none.
	Tags map[string]string
	// Priced splits Tokens by kind with each kind's list rate; nil when no
	// rate card prices the model. FOCUS 1.4 writes a row per kind.
	Priced []TokenPrice
}

// PlanUse is how a plan's windows were used over one charge period. A
// figure that was not measured is nil, never zero.
type PlanUse struct {
	// Start is the charge period's start, which names the period.
	Start time.Time
	// Window names the window kind Utilization is measured on; Windows
	// is how many of them reset in the period.
	Window  string
	Windows int
	// Utilization and Coverage are fractions from 0 to 1.
	Utilization *float64
	Coverage    *float64
}

// Period is a span of time, [From, To).
type Period struct{ From, To time.Time }

// ChargePeriods are the charge periods p is split into: its time in
// force, cut at calendar months.
func (p Purchase) ChargePeriods() []Period {
	from, to := p.From.UTC(), p.To.UTC()
	var out []Period
	for start := from; start.Before(to); {
		end := monthStart(start).AddDate(0, 1, 0)
		if end.After(to) {
			end = to
		}
		out = append(out, Period{From: start, To: end})
		start = end
	}
	return out
}

// Purchase is a plan's fee for the time it was in force.
type Purchase struct {
	Provider string
	Plan     Plan
	From, To time.Time
	// Amount is the fee for [From, To) in Currency.
	Amount   float64
	Currency string
	// Months is how many months of the plan Amount pays for.
	Months float64
	// ListPriced is true when Amount is the catalog's US list price, not
	// a price the operator gave.
	ListPriced bool
	// Use is the plan's window use per charge period (ChargePeriods),
	// which FOCUS 1.5 writes; a period without an entry wrote nothing.
	Use []PlanUse
}

// Row is one FOCUS charge. String fields left empty are null.
type Row struct {
	BilledCost         float64
	BillingAccountID   string
	BillingAccountName string
	BillingCurrency    string
	BillingPeriodStart time.Time
	BillingPeriodEnd   time.Time
	ChargeCategory     string
	ChargeClass        string
	ChargeDescription  string
	ChargeFrequency    string
	ChargePeriodStart  time.Time
	ChargePeriodEnd    time.Time
	// ConsumedQuantity is nil (null) unless the charge is Usage.
	ConsumedQuantity *int64
	ConsumedUnit     string
	ContractedCost   float64
	EffectiveCost    float64
	InvoiceIssuer    string
	ListCost         float64
	// pricingQuantity is the quantity in PricingUnit, as the exact
	// decimal it is written as.
	pricingQuantity    string
	PricingUnit        string
	ProviderName       string
	PublisherName      string
	ServiceCategory    string
	ServiceName        string
	ServiceSubcategory string
	SkuID              string
	// Tags is a JSON object, or empty for null: the operator's allocation
	// tags, the one user-defined tag scheme, so its keys are unprefixed.
	Tags string

	// The FOCUS 1.4 columns. A nil unit price is null.
	ContractedUnitPrice *float64
	ListUnitPrice       *float64
	SkuPriceID          string
	HostProviderName    string
	EstimatedCost       float64

	// The FOCUS 1.5 columns. SkuPriceDetails is a JSON object, or empty
	// for null.
	SkuMeter        string
	SkuPriceDetails string
	// How a plan's windows were used in the charge period, on its
	// Purchase rows; nil (and empty) is null.
	PlanWindowName  string
	PlanWindowCount *int64
	PlanUtilization *float64
	PlanCoverage    *float64
	PlanLapsedValue *float64

	// model groups a charge's rows when its SKU is one kind of token.
	model string
}

// Record is the row's cells in s's Columns order.
func (s Spec) Record(r Row) []string { return s.record(r) }

func (r Row) consumed() string {
	if r.ConsumedQuantity == nil {
		return ""
	}
	return strconv.FormatInt(*r.ConsumedQuantity, 10)
}

func (r Row) record12() []string {
	return []string{
		decimal(r.BilledCost),
		r.BillingAccountID,
		r.BillingAccountName,
		r.BillingCurrency,
		dateTime(r.BillingPeriodEnd),
		dateTime(r.BillingPeriodStart),
		r.ChargeCategory,
		r.ChargeClass,
		r.ChargeDescription,
		r.ChargeFrequency,
		dateTime(r.ChargePeriodEnd),
		dateTime(r.ChargePeriodStart),
		r.consumed(),
		r.ConsumedUnit,
		decimal(r.ContractedCost),
		decimal(r.EffectiveCost),
		r.InvoiceIssuer,
		decimal(r.ListCost),
		r.pricingQuantity,
		r.PricingUnit,
		r.ProviderName,
		r.PublisherName,
		r.ServiceCategory,
		r.ServiceName,
		r.ServiceSubcategory,
		r.SkuID,
		r.Tags,
	}
}

func (r Row) record14() []string {
	return []string{
		decimal(r.BilledCost),
		r.BillingAccountID,
		r.BillingAccountName,
		r.BillingCurrency,
		dateTime(r.BillingPeriodEnd),
		dateTime(r.BillingPeriodStart),
		r.ChargeCategory,
		r.ChargeClass,
		r.ChargeDescription,
		r.ChargeFrequency,
		dateTime(r.ChargePeriodEnd),
		dateTime(r.ChargePeriodStart),
		r.consumed(),
		r.ConsumedUnit,
		decimal(r.ContractedCost),
		optionalDecimal(r.ContractedUnitPrice),
		decimal(r.EffectiveCost),
		r.HostProviderName,
		r.InvoiceIssuer,
		decimal(r.ListCost),
		optionalDecimal(r.ListUnitPrice),
		r.pricingQuantity,
		r.PricingUnit,
		r.ServiceCategory,
		r.ServiceName,
		r.ProviderName,
		r.ServiceSubcategory,
		r.SkuID,
		r.SkuPriceID,
		r.Tags,
		decimal(r.EstimatedCost),
	}
}

func (r Row) record15() []string {
	var count string
	if r.PlanWindowCount != nil {
		count = strconv.FormatInt(*r.PlanWindowCount, 10)
	}
	return []string{
		decimal(r.BilledCost),
		r.BillingAccountID,
		r.BillingAccountName,
		r.BillingCurrency,
		dateTime(r.BillingPeriodEnd),
		dateTime(r.BillingPeriodStart),
		r.ChargeCategory,
		r.ChargeClass,
		r.ChargeDescription,
		r.ChargeFrequency,
		dateTime(r.ChargePeriodEnd),
		dateTime(r.ChargePeriodStart),
		r.consumed(),
		r.ConsumedUnit,
		decimal(r.ContractedCost),
		optionalDecimal(r.ContractedUnitPrice),
		decimal(r.EffectiveCost),
		r.HostProviderName,
		r.InvoiceIssuer,
		decimal(r.ListCost),
		optionalDecimal(r.ListUnitPrice),
		r.pricingQuantity,
		r.PricingUnit,
		r.ServiceCategory,
		r.ServiceName,
		r.ProviderName,
		r.ServiceSubcategory,
		r.SkuID,
		r.SkuMeter,
		r.SkuPriceDetails,
		r.SkuPriceID,
		r.Tags,
		decimal(r.EstimatedCost),
		optionalDecimal(r.PlanCoverage),
		optionalDecimal(r.PlanLapsedValue),
		optionalDecimal(r.PlanUtilization),
		count,
		r.PlanWindowName,
	}
}

// skuGroup is what orders a row among the charges: its model when its SKU
// is one kind of token, else its SKU.
func (r Row) skuGroup() string {
	if r.model != "" {
		return r.model
	}
	return r.SkuID
}

// Build maps usage and plan fees to FOCUS rows, ordered by charge period,
// then category, provider, SKU and SKU price.
func (s Spec) Build(usage []Usage, purchases []Purchase) []Row {
	out := make([]Row, 0, len(usage)+len(purchases))
	for _, u := range usage {
		out = append(out, s.usage(u)...)
	}
	for _, p := range purchases {
		out = append(out, s.purchase(p)...)
	}
	slices.SortStableFunc(out, func(a, b Row) int {
		return cmp.Or(a.ChargePeriodStart.Compare(b.ChargePeriodStart),
			cmp.Compare(a.ChargeCategory, b.ChargeCategory),
			cmp.Compare(a.ProviderName, b.ProviderName),
			cmp.Compare(a.skuGroup(), b.skuGroup()),
			cmp.Compare(a.BillingAccountID, b.BillingAccountID),
			cmp.Compare(a.Tags, b.Tags),
			cmp.Compare(kindOrder(a.SkuPriceID), kindOrder(b.SkuPriceID)))
	})
	return out
}

// WriteCSV writes the header and rows as CSV.
func (s Spec) WriteCSV(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(s.Columns); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(s.record(r)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func usageRows12(u Usage) []Row { return []Row{usageRow(u)} }

func usageRow(u Usage) Row {
	day := u.Day.UTC()
	tokens := u.Tokens
	r := base(u.Provider, day, day.Add(24*time.Hour))
	r.ChargeCategory = CategoryUsage
	r.ChargeFrequency = FrequencyUsageBased
	r.ConsumedQuantity = &tokens
	r.ConsumedUnit = unitTokens
	r.pricingQuantity = millions(tokens)
	r.PricingUnit = unitMillionTokens
	r.BillingCurrency = "USD"
	r.ListCost = u.ListUSD
	r.SkuID = u.Model
	if r.SkuID == "" {
		r.SkuID = "unidentified-model"
	}
	model := u.Model
	if model == "" {
		model = "a model the client did not name"
	}
	if u.PlanCovered {
		r.subscription(u.Provider, u.Plan)
		r.ChargeDescription = fmt.Sprintf("%s to %s, included in %s; ListCost is the API price",
			requests(u.Requests), model, r.ServiceName)
	} else {
		vendor := vendorName(u.Provider)
		r.BillingAccountID = u.Provider + "/pay-as-you-go"
		r.BillingAccountName = vendor + " pay-as-you-go"
		r.ServiceName = vendor + " API"
		r.BilledCost, r.EffectiveCost, r.ContractedCost = u.BilledUSD, u.BilledUSD, u.BilledUSD
		r.ChargeDescription = fmt.Sprintf("%s to %s, billed per token", requests(u.Requests), model)
	}
	if u.Unpriced {
		r.ChargeDescription += "; no API rate is known for some of it, so ListCost understates it"
	}
	r.Tags = tagsJSON(u.Tags)
	return r
}

// usageRows14 writes u as FOCUS 1.4 usage: a row per kind of token at its
// list rate, BilledCost and EffectiveCost 0, the estimate in
// x_EstimatedCost.
func usageRows14(u Usage) []Row {
	r := usageRow(u)
	r.HostProviderName = r.ProviderName
	r.BilledCost, r.EffectiveCost = 0, 0
	of := requests(u.Requests) + " to " + modelName(u.Model)
	how := "billed per token; x_EstimatedCost is TokenOps' estimate, BilledCost is left to the vendor's invoice"
	if u.PlanCovered {
		how = "included in " + r.ServiceName + "; ContractedCost is 0, ListCost is the API price"
	}
	understates := ""
	if u.Unpriced && !u.PlanCovered {
		understates = "; some of it has no recorded cost, so x_EstimatedCost understates it"
	}
	var priced []TokenPrice
	var list float64
	for _, p := range u.Priced {
		if p.Tokens > 0 {
			priced = append(priced, p)
			list += perMillion(p.Tokens, p.PerMillion)
		}
	}
	if len(priced) == 0 {
		tokens := u.Tokens
		r.ConsumedQuantity = &tokens
		r.SkuPriceID = r.SkuID + "/tokens"
		r.ListUnitPrice, r.ContractedUnitPrice = ptr(0.0), ptr(0.0)
		r.ListCost, r.ContractedCost = 0, 0
		r.EstimatedCost = estimate(u)
		r.ChargeDescription = fmt.Sprintf("Tokens of %s, %s; no API rate is known, so its unit prices are written as 0%s",
			of, how, understates)
		return []Row{r}
	}
	out := make([]Row, 0, len(priced))
	left := estimate(u)
	for i, p := range priced {
		k := r
		tokens := p.Tokens
		k.ConsumedQuantity = &tokens
		k.pricingQuantity = millions(tokens)
		k.SkuPriceID = r.SkuID + "/" + p.Kind
		contracted := p.PerMillion
		if u.PlanCovered {
			contracted = 0
		}
		k.ListUnitPrice, k.ContractedUnitPrice = ptr(p.PerMillion), ptr(contracted)
		k.ListCost = perMillion(tokens, p.PerMillion)
		k.ContractedCost = perMillion(tokens, contracted)
		// The estimate is split by each kind's share of the list price;
		// the last kind takes what rounding left.
		k.EstimatedCost = left
		if i < len(priced)-1 && list > 0 {
			k.EstimatedCost = estimate(u) * k.ListCost / list
			left -= k.EstimatedCost
		}
		k.ChargeDescription = fmt.Sprintf("%s tokens of %s, %s%s", kindName(p.Kind), of, how, understates)
		out = append(out, k)
	}
	return out
}

// estimate is what TokenOps estimates u cost: nothing for usage a plan
// covered.
func estimate(u Usage) float64 {
	if u.PlanCovered {
		return 0
	}
	return u.BilledUSD
}

// purchaseRows14 writes a plan's fee as FOCUS 1.4 purchases: BilledCost
// and EffectiveCost 0, the monthly fee as the unit price, the prorated fee
// in x_EstimatedCost.
func purchaseRows14(p Purchase) []Row {
	rows := purchaseRows(p)
	unit := 0.0
	if p.Months > 0 {
		unit = p.Amount / p.Months
	}
	for i := range rows {
		r := &rows[i]
		r.HostProviderName = r.ProviderName
		r.EstimatedCost = r.BilledCost
		r.BilledCost, r.EffectiveCost = 0, 0
		r.SkuPriceID = r.SkuID + "/monthly"
		r.ListUnitPrice, r.ContractedUnitPrice = ptr(unit), ptr(unit)
		months, _ := strconv.ParseFloat(r.pricingQuantity, 64)
		r.ListCost, r.ContractedCost = unit*months, unit*months
		r.ChargeDescription = strings.Replace(r.ChargeDescription, "prorated to the charge period",
			"prorated to the charge period in x_EstimatedCost; BilledCost is left to the vendor's invoice", 1)
	}
	return rows
}

// usageRows15 writes u as FOCUS 1.5 usage: the 1.4 rows, each kind of
// token its own SKU described in SkuPriceDetails, contracted at the list
// rate because no negotiated terms apply to it.
func usageRows15(u Usage) []Row {
	rows := usageRows14(u)
	for i := range rows {
		r := &rows[i]
		kind := strings.TrimPrefix(r.SkuPriceID, r.SkuID+"/")
		r.model = r.SkuID
		r.SkuID = r.SkuPriceID
		r.SkuMeter = skuMeter(kind)
		r.SkuPriceDetails = priceDetails(u.Provider, u.Model, kind)
		r.ContractedUnitPrice = ptr(*r.ListUnitPrice)
		r.ContractedCost = r.ListCost
		r.ChargeDescription = strings.Replace(r.ChargeDescription, "; ContractedCost is 0, ListCost is the API price",
			"; ListCost is the API price and x_EstimatedCost is 0", 1)
	}
	return rows
}

// purchaseRows15 writes a plan's fee as FOCUS 1.5 purchases: the 1.4 rows
// with a SkuMeter and, per charge period, how the plan's windows were
// used in it.
func purchaseRows15(p Purchase) []Row {
	rows := purchaseRows14(p)
	for i := range rows {
		r := &rows[i]
		r.SkuMeter = "Subscription"
		for _, use := range p.Use {
			if use.Start.Equal(r.ChargePeriodStart) {
				r.setPlanUse(use)
			}
		}
	}
	return rows
}

// setPlanUse fills the plan-window columns from use. The lapsed value is
// the share of the row's fee the unused part of its windows paid for.
func (r *Row) setPlanUse(use PlanUse) {
	if use.Coverage != nil {
		r.PlanCoverage = ptr(*use.Coverage)
	}
	if use.Utilization == nil || use.Window == "" {
		return
	}
	n := int64(use.Windows)
	r.PlanWindowName, r.PlanWindowCount = use.Window, &n
	util := min(max(*use.Utilization, 0), 1)
	r.PlanUtilization = &util
	r.PlanLapsedValue = ptr(r.EstimatedCost * (1 - util))
}

// skuMeter names what a token SKU meters, in the wording FOCUS's examples
// use.
func skuMeter(kind string) string {
	switch kind {
	case TokensInput:
		return "Input Tokens"
	case TokensCacheRead:
		return "Cache Read Input Tokens"
	case TokensCacheWrite, TokensCacheWrite1h:
		return "Cache Write Input Tokens"
	case TokensOutput:
		return "Output Tokens"
	}
	return "Tokens"
}

// priceDetails is a token SKU's SkuPriceDetails: the model, who developed
// it where that is known, and the direction and cache action of the
// tokens it meters. A SKU that lumps every kind of token (no rate card
// split it) has neither a direction nor a cache action.
func priceDetails(provider, model, kind string) string {
	d := map[string]string{}
	if model != "" {
		d["ModelId"] = model
	}
	if dev := modelDeveloper(provider, model); dev != "" {
		d["ModelDeveloper"] = dev
	}
	switch kind {
	case TokensInput:
		d["TokenDirection"], d["TokenCacheAction"] = "Input", "Uncached"
	case TokensCacheRead:
		d["TokenDirection"], d["TokenCacheAction"] = "Input", "Read"
	case TokensCacheWrite:
		d["TokenDirection"], d["TokenCacheAction"], d["x_CacheRetention"] = "Input", "Write", "5 Minutes"
	case TokensCacheWrite1h:
		d["TokenDirection"], d["TokenCacheAction"], d["x_CacheRetention"] = "Input", "Write", "1 Hour"
	case TokensOutput:
		d["TokenDirection"] = "Output"
	}
	if len(d) == 0 {
		return ""
	}
	return tagsJSON(d)
}

// firstPartyDevelopers are the providers that serve only models they
// developed themselves.
var firstPartyDevelopers = map[string]string{
	"anthropic": "Anthropic",
	"openai":    "OpenAI",
	"gemini":    "Google",
}

// namespaceDevelopers map the developer namespace a router puts before a
// model ("anthropic/claude-opus-5") to the developer.
var namespaceDevelopers = map[string]string{
	"anthropic":  "Anthropic",
	"openai":     "OpenAI",
	"google":     "Google",
	"meta-llama": "Meta",
	"mistralai":  "Mistral AI",
	"deepseek":   "DeepSeek",
	"x-ai":       "xAI",
}

// modelDeveloper is who developed model, or empty when that is not known:
// the provider for a first-party API, or the namespace a router names.
func modelDeveloper(provider, model string) string {
	if model == "" {
		return ""
	}
	if dev, ok := firstPartyDevelopers[provider]; ok {
		return dev
	}
	if ns, _, ok := strings.Cut(model, "/"); ok {
		return namespaceDevelopers[strings.ToLower(ns)]
	}
	return ""
}

// kinds orders a charge's rows by kind of token.
var kinds = []string{TokensInput, TokensCacheRead, TokensCacheWrite, TokensCacheWrite1h, TokensOutput}

func kindOrder(skuPriceID string) int {
	for i, k := range kinds {
		if strings.HasSuffix(skuPriceID, "/"+k) {
			return i
		}
	}
	return len(kinds)
}

func kindName(kind string) string {
	switch kind {
	case TokensInput:
		return "Input"
	case TokensCacheRead:
		return "Cache-read"
	case TokensCacheWrite:
		return "Cache-write (5-minute)"
	case TokensCacheWrite1h:
		return "Cache-write (1-hour)"
	case TokensOutput:
		return "Output"
	}
	return kind
}

func modelName(model string) string {
	if model == "" {
		return "a model the client did not name"
	}
	return model
}

func ptr(v float64) *float64 { return &v }

// perMillion is tokens at a rate per million tokens.
func perMillion(tokens int64, rate float64) float64 {
	return float64(tokens) * rate / 1_000_000
}

// optionalDecimal writes v as decimal, or empty for null.
func optionalDecimal(v *float64) string {
	if v == nil {
		return ""
	}
	return decimal(*v)
}

// tagsJSON writes tags as a JSON object, keys sorted; empty for none.
func tagsJSON(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return ""
	}
	return string(b)
}

func purchaseRows(p Purchase) []Row {
	from, to := p.From.UTC(), p.To.UTC()
	total := to.Sub(from)
	if total <= 0 {
		return nil
	}
	var out []Row
	for start := from; start.Before(to); {
		end := monthStart(start).AddDate(0, 1, 0)
		if end.After(to) {
			end = to
		}
		share := float64(end.Sub(start)) / float64(total)
		amount := p.Amount * share
		r := base(p.Provider, start, end)
		r.subscription(p.Provider, p.Plan)
		r.ChargeCategory = CategoryPurchase
		r.ChargeFrequency = FrequencyRecurring
		r.BillingCurrency = strings.ToUpper(strings.TrimSpace(p.Currency))
		if r.BillingCurrency == "" {
			r.BillingCurrency = "USD"
		}
		r.BilledCost, r.EffectiveCost, r.ContractedCost, r.ListCost = amount, amount, amount, amount
		r.pricingQuantity = decimalPlaces(p.Months*share, 6)
		r.PricingUnit = unitMonths
		r.SkuID = p.Plan.ID
		r.ChargeDescription = r.ServiceName + " subscription fee, prorated to the charge period"
		if p.ListPriced {
			r.ChargeDescription += "; priced at the US list price, as no price was given"
		}
		out = append(out, r)
		start = end
	}
	return out
}

// base is a row's provider and period columns.
func base(provider string, from, to time.Time) Row {
	vendor := vendorName(provider)
	return Row{
		BillingPeriodStart: monthStart(from),
		BillingPeriodEnd:   monthStart(from).AddDate(0, 1, 0),
		ChargePeriodStart:  from,
		ChargePeriodEnd:    to,
		InvoiceIssuer:      vendor,
		ProviderName:       vendor,
		PublisherName:      vendor,
		ServiceCategory:    serviceCategory,
		ServiceSubcategory: serviceSubcategory,
	}
}

// subscription sets the account and service of a plan's charges.
func (r *Row) subscription(provider string, plan Plan) {
	r.BillingAccountID = provider + "/subscription"
	r.BillingAccountName = vendorName(provider) + " subscription"
	r.ServiceName = plan.Display
	if r.ServiceName == "" {
		r.ServiceName = providers.DisplayName(provider) + " subscription"
	}
}

// vendors names the company behind a provider whose registry name is a
// product: FOCUS's ProviderName is the entity that sells the service.
var vendors = map[string]string{
	"anthropic": "Anthropic",
	"openai":    "OpenAI",
	"gemini":    "Google",
	"github":    "GitHub",
}

func vendorName(provider string) string {
	if v, ok := vendors[provider]; ok {
		return v
	}
	if provider == "" {
		return "Unknown provider"
	}
	return providers.DisplayName(provider)
}

func requests(n int64) string {
	if n == 1 {
		return "1 request"
	}
	return strconv.FormatInt(n, 10) + " requests"
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// dateTime is FOCUS's date/time format: extended ISO 8601 in UTC.
func dateTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// decimal writes v as FOCUS's numeric format: plain decimal, no sign on
// positives, to ten places at most.
func decimal(v float64) string {
	v = math.Round(v*1e10) / 1e10
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// decimalPlaces writes v rounded to n places, trailing zeros dropped.
func decimalPlaces(v float64, n int) string {
	p := math.Pow10(n)
	return decimal(math.Round(v*p) / p)
}

// millions writes tokens / 1,000,000 exactly.
func millions(tokens int64) string {
	whole, frac := tokens/1_000_000, tokens%1_000_000
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." + strings.TrimRight(fmt.Sprintf("%06d", frac), "0")
}
