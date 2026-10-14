// Package focus renders spend and usage as a FOCUS dataset: the FinOps
// Open Cost and Usage Specification that FinOps tools (Vantage, CloudZero,
// Cloudability and others) ingest. It targets FOCUS 1.2, whose column
// definitions and allowed values the conformance test transcribes from
// the specification (github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec,
// tag v1.2).
//
// The mapping:
//
//   - A plan's fee is a Purchase, Recurring, prorated over the days it was
//     in force and split at calendar months, which stand in for billing
//     periods. A subscription is not a commitment discount, so its
//     EffectiveCost is what it costs.
//   - Usage a plan covered is a Usage row billed 0, with ListCost at the
//     API price: ListCost against EffectiveCost is the value the plan gave.
//   - Usage billed per token is a Usage row that costs what it cost.
//   - Tokens are ConsumedQuantity in "Tokens"; PricingQuantity counts
//     millions of tokens, the unit API prices are published in.
//
// Only derived fields leave: providers, models, plans, counts and money.
// No session title, path or prompt has a column to go in.
package focus

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// Version is the FOCUS version the dataset conforms to.
const Version = "1.2"

// Columns are the dataset's columns, in the alphabetical order the FOCUS
// examples use: every Mandatory column of FOCUS 1.2, plus the Recommended
// and Conditional ones TokenOps can fill truthfully.
var Columns = []string{
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

// Allowed values used here, as FOCUS 1.2 spells them.
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
	// ID is the catalog name ("claude-max-5x").
	ID string
	// Display is what the vendor calls it ("Claude Max 5x"); empty when
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
	// Tags is a JSON object, or empty for null. Nothing fills it yet:
	// user-defined tags come with allocation tags.
	Tags string
}

// Record is the row's cells, in Columns order.
func (r Row) Record() []string {
	consumed := ""
	if r.ConsumedQuantity != nil {
		consumed = strconv.FormatInt(*r.ConsumedQuantity, 10)
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
		consumed,
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

// Build maps usage and plan fees to FOCUS rows, ordered by charge period,
// then category, provider and SKU.
func Build(usage []Usage, purchases []Purchase) []Row {
	out := make([]Row, 0, len(usage)+len(purchases))
	for _, u := range usage {
		out = append(out, usageRow(u))
	}
	for _, p := range purchases {
		out = append(out, purchaseRows(p)...)
	}
	slices.SortStableFunc(out, func(a, b Row) int {
		return cmp.Or(a.ChargePeriodStart.Compare(b.ChargePeriodStart),
			cmp.Compare(a.ChargeCategory, b.ChargeCategory),
			cmp.Compare(a.ProviderName, b.ProviderName),
			cmp.Compare(a.SkuID, b.SkuID),
			cmp.Compare(a.BillingAccountID, b.BillingAccountID))
	})
	return out
}

// WriteCSV writes the header and rows as CSV.
func WriteCSV(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(Columns); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(r.Record()); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

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
	return r
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
