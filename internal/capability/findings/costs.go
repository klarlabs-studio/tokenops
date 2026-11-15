package findings

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// CostSource reads what each session's turns cost at list prices, as of a
// session analysis's start, through the store's incremental aggregator. A
// nil source leaves the effort rows' costs out.
type CostSource func(ctx context.Context, now time.Time) agentdx.CostOf

// SessionCosts is the cost source the daemon's session analysis reads
// through: the aggregator's turns over the analysed week. Nil without an
// aggregator.
func SessionCosts(agg *analytics.Aggregator) CostSource {
	if agg == nil {
		return nil
	}
	return func(ctx context.Context, now time.Time) agentdx.CostOf {
		turns, err := agg.SessionTurns(ctx, analytics.Filter{Since: now.AddDate(0, 0, -SessionDays), Until: now})
		if err != nil {
			return nil
		}
		return CostOfTurns(turns)
	}
}

// At is the cost of the units as of now; nil when the source is nil or
// reads fail, which leaves the effort costs out.
func (c CostSource) At(ctx context.Context, now time.Time) agentdx.CostOf {
	if c == nil {
		return nil
	}
	return c(ctx, now)
}

// CostOfTurns prices a unit from the turns of its session, at list prices.
func CostOfTurns(turns []analytics.SessionTurn) agentdx.CostOf {
	by := map[string][]agentdx.TurnCost{}
	for _, t := range turns {
		by[t.SessionID] = append(by[t.SessionID], agentdx.TurnCost{At: t.At, USD: t.APIEquivalentUSD})
	}
	return agentdx.UnitCosts(by)
}
