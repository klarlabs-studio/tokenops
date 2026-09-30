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
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// History is where plan changes are kept.
type History interface {
	Load() (plans.History, error)
	Append(...plans.Binding) error
}

// Restamper re-marks recorded usage as covered by a plan.
type Restamper interface {
	RestampPlanIncluded(ctx context.Context, provider string, from, to time.Time) (int64, error)
}

// Result is what a switch changed.
type Result struct {
	Recorded []plans.Binding `json:"recorded"`
	// Restamped counts usage re-marked from billed to plan-covered.
	Restamped int64 `json:"restamped"`
}

// ErrFuture refuses a start date after now: a plan that has not started
// is not in force, and recording it would make every report until then
// wrong.
var ErrFuture = errors.New("planswitch: the start date is in the future")

// Record writes a switch of provider from previous to plan, effective
// from `from`. When from is before now and the new plan is not empty,
// usage recorded as billed per token since then is re-marked as covered.
// A nil restamper skips that step (no event store), which the caller
// reports.
func Record(ctx context.Context, h History, r Restamper, provider, previous, plan string, from, now time.Time) (Result, error) {
	if from.After(now) {
		return Result{}, ErrFuture
	}
	past, err := h.Load()
	if err != nil {
		return Result{}, err
	}
	bs := past.Switch(provider, previous, plan, from, now)
	if err := h.Append(bs...); err != nil {
		return Result{}, err
	}
	res := Result{Recorded: bs}
	if plan == "" || r == nil || !from.Before(now) {
		return res, nil
	}
	n, err := r.RestampPlanIncluded(ctx, provider, from, now)
	if err != nil {
		return res, err
	}
	res.Restamped = n
	return res, nil
}

// PeriodCost is one stretch on one plan, priced.
type PeriodCost struct {
	plans.Period
	Display string  `json:"display"`
	USD     float64 `json:"usd"`
	// Priced is false when the catalog has no flat price for the plan.
	Priced bool `json:"priced"`
}

// ProviderCost is a provider's plans over a period.
type ProviderCost struct {
	Provider string       `json:"provider"`
	Periods  []PeriodCost `json:"periods"`
	USD      float64      `json:"usd"`
	// Complete is false when a period's plan has no price, so USD
	// understates what was paid.
	Complete bool `json:"complete"`
}

// Cost prices every provider's plans over [since, until), prorated by
// day across switches. current is the configured binding per provider.
// Per-seat plans are priced for one seat.
func Cost(h plans.History, current map[string]string, since, until time.Time) []ProviderCost {
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
			if p, ok := plans.Lookup(period.Plan); ok {
				c.Display = p.Display
				if p.MonthlyUSD > 0 {
					c.USD, c.Priced = plans.PeriodCost(p.MonthlyUSD, period), true
				}
			}
			if !c.Priced {
				pc.Complete = false
			}
			pc.USD += c.USD
			pc.Periods = append(pc.Periods, c)
		}
		if len(pc.Periods) > 0 {
			out = append(out, pc)
		}
	}
	return out
}

// Total sums the providers' plan cost.
func Total(costs []ProviderCost) (usd float64, complete bool) {
	complete = true
	for _, c := range costs {
		usd += c.USD
		complete = complete && c.Complete
	}
	return usd, complete
}
