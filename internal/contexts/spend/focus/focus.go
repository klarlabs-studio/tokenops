// Package focus renders spend and usage as a FOCUS dataset: the FinOps
// Open Cost and Usage Specification that FinOps tools (Vantage, CloudZero,
// Cloudability and others) ingest. It writes FOCUS 1.4, the default, and
// FOCUS 1.2 for tools that do not read 1.4 yet. The conformance tests
// transcribe each version's column definitions and allowed values from
// the specification (github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec,
// tags v1.2 and v1.4).
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
}

// V1_2 and V1_4 are the versions the export writes.
var (
	V1_2 = Spec{Version: "1.2", Columns: columns12, usage: usageRows12, purchase: purchaseRows, record: Row.record12}
	V1_4 = Spec{Version: "1.4", Columns: columns14, usage: usageRows14, purchase: purchaseRows14, record: Row.record14}
)

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
	}
	return Spec{}, fmt.Errorf("%w: %q; use %s or %s", ErrUnsupportedVersion, version, V1_4.Version, V1_2.Version)
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
			cmp.Compare(a.SkuID, b.SkuID),
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
