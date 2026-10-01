// Package planswitch changes a provider's plan with its history, and
// prices plans over a period.
//
// The configuration holds the plan in force now. Every switch is also
// recorded with the date it took effect, so a report over a past period
// uses the plan in force then (ADR 0008). A switch backdated with a
// start date also corrects the usage recorded as billed per token in
// between: the operator is saying they were on the plan all along.
package planswitch

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// History is where plan changes are kept.
type History interface {
	Load() (plans.History, error)
	Append(...plans.Binding) error
}

// Restamper re-marks recorded usage as covered by a plan, or as billed
// per token for a plan that covers nothing (usage-based Enterprise).
type Restamper interface {
	RestampPlanIncluded(ctx context.Context, provider string, from, to time.Time) (int64, error)
	RestampMetered(ctx context.Context, provider string, from, to time.Time) (int64, error)
}

// Result is what a switch changed.
type Result struct {
	Recorded []plans.Binding `json:"recorded"`
	// Restamped counts usage re-marked: from billed to plan-covered, or,
	// when the plan is billed at API rates, from plan-covered to billed.
	Restamped int64 `json:"restamped"`
	// RestampedTo is the cost source that usage now carries.
	RestampedTo string `json:"restamped_to,omitempty"`
}

// ErrFuture refuses a start date after now: a plan that has not started
// is not in force, and recording it would make every report until then
// wrong.
var ErrFuture = errors.New("planswitch: the start date is in the future")

// Change is one plan switch: provider moves from Previous to Plan from
// From on, paying Price in Currency per month when the operator gave it.
type Change struct {
	Provider, Previous, Plan string
	From, Now                time.Time
	Price                    float64
	Currency                 string
}

// Record writes a switch of provider from previous to plan, effective
// from `from`. When from is before now and the new plan is not empty,
// usage since then is re-marked to match the plan: covered for a
// subscription, billed per token for a plan billed at API rates.
// A nil restamper skips that step (no event store), which the caller
// reports.
func Record(ctx context.Context, h History, r Restamper, c Change) (Result, error) {
	provider, plan, from, now := c.Provider, c.Plan, c.From, c.Now
	if from.After(now) {
		return Result{}, ErrFuture
	}
	past, err := h.Load()
	if err != nil {
		return Result{}, err
	}
	bs := past.Switch(provider, c.Previous, plan, from, now)
	last := &bs[len(bs)-1]
	last.Price, last.Currency = c.Price, strings.ToUpper(strings.TrimSpace(c.Currency))
	if err := h.Append(bs...); err != nil {
		return Result{}, err
	}
	res := Result{Recorded: bs}
	if plan == "" || r == nil || !from.Before(now) {
		return res, nil
	}
	restamp, to := r.RestampPlanIncluded, "plan_included"
	if !plans.Covers(plan) {
		restamp, to = r.RestampMetered, "metered"
	}
	n, err := restamp(ctx, provider, from, now)
	if err != nil {
		return res, err
	}
	res.Restamped, res.RestampedTo = n, to
	return res, nil
}

// FX is the currency plan costs are shown in and how to reach it from
// US dollars. The rate is the operator's: TokenOps fetches none.
type FX struct {
	// Currency is the ISO 4217 code costs are shown in; empty is USD.
	Currency string
	// PerUSD is how many units of Currency one US dollar buys. Ignored
	// for USD.
	PerUSD float64
}

func (f FX) code() string {
	if c := strings.ToUpper(strings.TrimSpace(f.Currency)); c != "" {
		return c
	}
	return "USD"
}

// convert brings amount in currency into f's currency, or reports that
// it cannot without a rate.
func (f FX) convert(amount float64, currency string) (float64, bool) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "USD"
	}
	switch {
	case currency == f.code():
		return amount, true
	case currency == "USD" && f.PerUSD > 0:
		return amount * f.PerUSD, true
	}
	return 0, false
}

// FromUSD converts a US-dollar amount into f's currency.
func (f FX) FromUSD(usd float64) (float64, bool) { return f.convert(usd, "USD") }

// PeriodCost is one stretch on one plan, priced.
type PeriodCost struct {
	plans.Period
	Display string `json:"display"`
	// Amount is the period's cost in the report's currency.
	Amount float64 `json:"amount"`
	// Source says where the price came from: "yours" (what the operator
	// said they pay) or "list" (the catalog's US list price).
	Source string `json:"source,omitempty"`
	// Priced is false when there is no price for the plan, or it is in a
	// currency the report has no rate for.
	Priced bool `json:"priced"`
}

// ProviderCost is a provider's plans over a period.
type ProviderCost struct {
	Provider string       `json:"provider"`
	Periods  []PeriodCost `json:"periods"`
	Amount   float64      `json:"amount"`
	// Complete is false when a period could not be priced, so Amount
	// understates what was paid.
	Complete bool `json:"complete"`
}

// Cost prices every provider's plans over [since, until), prorated by
// day across switches, in fx's currency. The operator's own price wins;
// the catalog's US list price is the fallback. Per-seat plans are priced
// for one seat.
func Cost(h plans.History, current map[string]string, since, until time.Time, fx FX) []ProviderCost {
	providers := map[string]bool{}
	for p, plan := range current {
		if plan != "" {
			providers[p] = true
		}
	}
	for _, b := range h {
		providers[b.Provider] = true
	}
	names := make([]string, 0, len(providers))
	for p := range providers {
		names = append(names, p)
	}
	sort.Strings(names)
	var out []ProviderCost
	for _, provider := range names {
		pc := ProviderCost{Provider: provider, Complete: true}
		for _, period := range h.Periods(provider, since, until, current[provider]) {
			c := PeriodCost{Period: period}
			monthly, currency := period.Price, period.Currency
			c.Source = "yours"
			if p, ok := plans.Lookup(period.Plan); ok {
				c.Display = p.Display
				if monthly <= 0 && p.MonthlyUSD > 0 {
					monthly, currency, c.Source = p.MonthlyUSD, "USD", "list"
				}
			}
			if monthly > 0 {
				if amount, ok := fx.convert(plans.PeriodCost(monthly, period), currency); ok {
					c.Amount, c.Priced = amount, true
				}
			}
			if !c.Priced {
				c.Source = ""
				pc.Complete = false
			}
			pc.Amount += c.Amount
			pc.Periods = append(pc.Periods, c)
		}
		if len(pc.Periods) > 0 {
			out = append(out, pc)
		}
	}
	return out
}

// Total sums the providers' plan cost.
func Total(costs []ProviderCost) (amount float64, complete bool) {
	complete = true
	for _, c := range costs {
		amount += c.Amount
		complete = complete && c.Complete
	}
	return amount, complete
}
