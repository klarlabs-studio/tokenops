package analytics

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// Charge is one UTC day's usage of one provider's model, accounted one
// way: covered by a plan, or billed per token. It is the grain a billing
// export reports at.
type Charge struct {
	// Day is the UTC day the usage happened on.
	Day      time.Time
	Provider string
	Model    string
	// PlanCovered is true for plan-included and trial usage, which was
	// billed nothing at the margin.
	PlanCovered bool
	Requests    int64
	// InputTokens includes cache reads and writes.
	InputTokens, OutputTokens int64
	// CostUSD is what was billed: zero for plan-covered usage, the stored
	// or recomputed cost otherwise.
	CostUSD float64
	// ListUSD is the usage at API list prices. For metered usage it
	// equals CostUSD.
	ListUSD float64
	// Unpriced is true when some of the usage has no rate card, so
	// ListUSD (and, for metered usage, CostUSD) understates it.
	Unpriced bool
}

// Tokens is the charge's input and output tokens.
func (c Charge) Tokens() int64 { return c.InputTokens + c.OutputTokens }

// DailyCharges splits the window's usage per UTC day, provider and model
// into the plan-covered part and the metered part. The charges sum to
// what Summarize reports for the same window: requests, cost and API
// equivalent.
func (a *Aggregator) DailyCharges(ctx context.Context, f Filter) ([]Charge, error) {
	if !a.ready() {
		return nil, ErrNotInitialised
	}
	width := BucketDay.seconds()
	usage, err := a.store.BucketUsage(ctx, f, width, GroupProviderModel)
	if err != nil {
		return nil, err
	}
	rows := usage.Rows
	for i := range rows {
		rows[i].Cost = storedCostValue(rows[i], a.spend != nil)
	}
	if a.spend != nil {
		a.recomputeMissingCosts(usage.Uncosted, rows)
	}
	plan := a.planCoveredCharges(usage.PlanCovered)

	out := make([]Charge, 0, len(rows)+len(plan))
	for _, r := range rows {
		provider, model := SplitProviderModelKey(r.GroupKey)
		k := rowKey{r.BucketStart.Unix(), r.GroupKey}
		p, covered := plan[k]
		if covered {
			out = append(out, p)
		}
		metered := Charge{
			Day: r.BucketStart, Provider: provider, Model: model,
			Requests:     r.Requests - p.Requests,
			InputTokens:  r.InputTokens - p.InputTokens,
			OutputTokens: r.OutputTokens - p.OutputTokens,
			CostUSD:      r.CostUSD,
			ListUSD:      r.CostUSD,
			Unpriced:     !r.Cost.Complete(),
		}
		if metered.Requests > 0 {
			out = append(out, metered)
		}
	}
	slices.SortStableFunc(out, func(x, y Charge) int {
		return cmp.Or(x.Day.Compare(y.Day), cmp.Compare(x.Provider, y.Provider), cmp.Compare(x.Model, y.Model))
	})
	return out, nil
}

// planCoveredCharges sums the plan-covered pricing groups per row and
// values them at list price.
func (a *Aggregator) planCoveredCharges(groups []PricingGroup) map[rowKey]Charge {
	out := map[rowKey]Charge{}
	for _, g := range groups {
		k := rowKey{g.At.Unix(), g.GroupKey}
		provider, model := SplitProviderModelKey(g.GroupKey)
		c := out[k]
		c.Day, c.Provider, c.Model, c.PlanCovered = g.At, provider, model, true
		c.Requests += g.Events
		c.InputTokens += g.InputTokens
		c.OutputTokens += g.OutputTokens
		if a.spend == nil {
			c.Unpriced = true
		} else if v, err := a.spend.ComputeAt(g.promptEvent(), g.At); err != nil {
			c.Unpriced = true
		} else {
			c.ListUSD += v
		}
		out[k] = c
	}
	return out
}
