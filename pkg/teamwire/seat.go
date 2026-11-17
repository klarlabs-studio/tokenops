package teamwire

import (
	"math"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// Seat actions: what a plan advice row means for the seat it was
// computed for. The mapping lives here, in public code, so every surface
// that labels a seat (tokenops team preview and the team server alike)
// labels it the same way.
const (
	// SeatKeep is a seat that fits, a saving too small to act on, or a
	// cancellation the guard vetoed.
	SeatKeep = "keep"
	// SeatMoveDown is a cheaper tier with a smaller allowance, on its own
	// or with its overflow billed per token.
	SeatMoveDown = "move_down"
	// SeatUpgrade is a larger tier: the plan in force ran out too often.
	SeatUpgrade = "upgrade"
	// SeatCancel is no subscription: paying per token at list price is
	// materially cheaper than the fee, the limit is barely used, and the
	// seat is the company's to cancel.
	SeatCancel = "cancel"
)

// SeatCancelMaxLimitUsedPct is the most of its limit, in points, a seat may
// use and still be recommended for cancelling. The value a seat carries
// counts only usage TokenOps sees; use elsewhere (a web chat, an
// unenrolled machine) fills the vendor's meter but carries no value. A
// seat with little value and a well-used limit is used outside the
// harness, not idle.
const SeatCancelMaxLimitUsedPct = 10.0

// SeatAction is the action a plan advice row recommends for its seat.
// limitUsedPct is the mean peak of the plan's longest window kind over the
// period; NaN or a negative value when no meter measured it, which never
// permits a cancellation. companyPaid says the seat is one the
// organisation pays for: without it, cancel is never recommended, since a
// person's own subscription is not the organisation's to cancel.
func SeatAction(a PlanAdvice, limitUsedPct float64, companyPaid bool) string {
	switch a.Verdict {
	case "upgrade":
		return SeatUpgrade
	case "switch":
	default:
		return SeatKeep
	}
	if !a.Material {
		return SeatKeep
	}
	switch a.Kind {
	case "api":
		measured := !math.IsNaN(limitUsedPct) && limitUsedPct >= 0
		if companyPaid && measured && limitUsedPct < SeatCancelMaxLimitUsedPct {
			return SeatCancel
		}
	case "plan", "plan_overflow":
		if PlanFactor(a.RecommendedPlan) < PlanFactor(a.Plan) {
			return SeatMoveDown
		}
	}
	return SeatKeep
}

// LapseMinLeftPct is the share of a window, in points, that resetting
// unused makes a lapse: a weekly window projected to reset with no more
// than 100 minus this used will reset mostly unused. It is the threshold
// the member's own lapse finding uses, so the team view and the member's
// machine call the same week a lapse.
const LapseMinLeftPct = plans.LapseMinLeftPct

// SpendDenominated reports whether plan is billed per token against a
// spend limit rather than a seat fee with usage windows: pay-as-you-go
// and usage-based Enterprise. Such a seat has no tier to move and no
// window to project; its use is spend at API rates. It reads the plan
// catalog, by a plan's current name or one the vendor retired; a name the
// catalog does not know is false.
func SpendDenominated(plan string) bool {
	p, ok := plans.Lookup(CurrentPlan(plan))
	return ok && p.SpendDenominated
}

// PlanFactor is a catalog plan's weekly allowance as a multiple of its
// family's base plan (Claude Max is 5, Ultra 10 times Pro's week); zero for
// a plan with no stated multiple, one billed by spend, and any name the
// catalog does not know. A name the vendor retired is the plan's current
// one.
func PlanFactor(plan string) float64 {
	p, ok := catalogPlan(plan)
	if !ok || p.SpendDenominated {
		return 0
	}
	if p.RelativeTo != "" {
		return weeklyFactor(p)
	}
	for _, q := range providers.Plans() {
		if q.RelativeTo == p.Name && weeklyFactor(q) > 0 {
			return 1
		}
	}
	return 0
}

// ListPriceUSD is a catalog plan's published monthly list price in US
// dollars, and whether it is per seat. ok is false when the catalog has no
// flat price for it (Enterprise, a tier not specific enough) or does not
// know the plan. A name the vendor retired is priced as the plan's current
// one.
func ListPriceUSD(plan string) (usd float64, perSeat, ok bool) {
	p, known := catalogPlan(plan)
	if !known || p.MonthlyUSD <= 0 {
		return 0, false, false
	}
	return p.MonthlyUSD, p.PerSeat, true
}

// catalogPlan is the catalog entry for plan, by its current name or one
// the vendor retired.
func catalogPlan(plan string) (providers.Plan, bool) {
	name := CurrentPlan(plan)
	for _, p := range providers.Plans() {
		if p.Name == name {
			return p, true
		}
	}
	return providers.Plan{}, false
}

// weeklyFactor is p's weekly allowance as a multiple of its base's.
func weeklyFactor(p providers.Plan) float64 {
	if p.WeeklyMultiplier > 0 {
		return p.WeeklyMultiplier
	}
	return p.Multiplier
}
