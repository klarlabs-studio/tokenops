package analytics

import (
	"cmp"
	"context"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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
	// InputTokens includes cache reads and writes: CacheReadTokens read
	// from the prompt cache, CacheWriteTokens written to it
	// (CacheWrite1hTokens of them at the one-hour rate).
	InputTokens, OutputTokens         int64
	CacheReadTokens, CacheWriteTokens int64
	CacheWrite1hTokens                int64
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
			Requests:           r.Requests - p.Requests,
			InputTokens:        r.InputTokens - p.InputTokens,
			OutputTokens:       r.OutputTokens - p.OutputTokens,
			CacheReadTokens:    r.CacheReadTokens - p.CacheReadTokens,
			CacheWriteTokens:   r.CacheWriteTokens - p.CacheWriteTokens,
			CacheWrite1hTokens: r.CacheWrite1hTokens - p.CacheWrite1hTokens,
			CostUSD:            r.CostUSD,
			ListUSD:            r.CostUSD,
			Unpriced:           !r.Cost.Complete(),
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
		c.CacheReadTokens += g.CacheReadTokens
		c.CacheWriteTokens += g.CacheWriteTokens
		c.CacheWrite1hTokens += g.CacheWrite1hTokens
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

// RateAt is the list rate of provider's model on the rate card in effect
// at at; false when the aggregator has no rate card or the card has no
// rate for the model.
func (a *Aggregator) RateAt(provider, model string, at time.Time) (spend.Rate, bool) {
	if a.spend == nil {
		return spend.Rate{}, false
	}
	rate, err := a.spend.TableAt(at).Lookup(eventschema.Provider(provider), model)
	if err != nil {
		return spend.Rate{}, false
	}
	return rate, true
}
