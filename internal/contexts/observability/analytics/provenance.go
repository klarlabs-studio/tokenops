package analytics

import "go.klarlabs.de/tokenops/internal/contexts/measurement"

// sourceEvents names the local event store as the origin of a figure.
const sourceEvents = "sqlite_events"

// storedCostValue is the provenance of a row's cost before any rate-card
// recompute has run.
//
// A stored zero is the ambiguous case. It means either "this genuinely
// cost nothing" or "nothing has priced this yet", and with no spend
// engine wired there is no way to tell — so the value says unknown rather
// than handing out a zero that reads like an answer. With an engine
// present, recomputeMissingCosts is about to resolve it and replace this.
func storedCostValue(r Row, canRecompute bool) measurement.Value {
	if r.CostUSD == 0 && !canRecompute {
		return measurement.Unknown(
			"no pricing table is wired, so a stored cost of 0 cannot be told apart from an unpriced model").
			At(r.BucketStart)
	}
	return measurement.Measured(r.CostUSD, sourceEvents).
		At(r.BucketStart).
		Covering(r.Requests, 0)
}

// recomputedCostValue is the provenance of a row's cost after the spend
// engine has priced whatever the store left at zero.
//
// Quality drops to derived once any part of the figure came from a rate
// card rather than from what the vendor recorded: the two are not the
// same claim, and a surface that presents a recomputed total as billed
// fact is overstating what TokenOps knows.
//
// missing counts the events whose model has no rate card. They contribute
// nothing to the amount — failing the whole rollup over one missing rate
// card would take the figures that do work with it — but they are counted
// against coverage, so the total can no longer present itself as
// complete.
func recomputedCostValue(r Row, missing int64, gaps []string) measurement.Value {
	included := r.Requests - missing
	if included < 0 {
		included = 0
	}

	v := measurement.Measured(r.CostUSD, sourceEvents)
	if r.CostRecomputed > 0 {
		v = measurement.Derived(r.CostUSD, sourceEvents).
			WithCaveat("part of this figure was priced from the rate card, not recorded by the vendor")
	}
	return v.At(r.BucketStart).Covering(included, missing, gaps...)
}

// equivalentValue is the provenance of APIEquivalentUSD: what the row
// would have billed at list prices, including traffic a flat-rate plan
// absorbed.
//
// It is always derived. Nobody was charged this; it is a counterfactual
// computed from a rate card, and calling it measured would invite exactly
// the reading — "we spent this" — that ADR 0003 exists to prevent.
func equivalentValue(r Row, missing int64, gaps []string) measurement.Value {
	included := r.Requests - missing
	if included < 0 {
		included = 0
	}
	return measurement.Derived(r.APIEquivalentUSD, sourceEvents).
		WithCaveat("list-price equivalent, not an amount billed").
		At(r.BucketStart).
		Covering(included, missing, gaps...)
}

// gapTracker accumulates, per aggregation key, the events that could not
// be priced and the distinct reasons why.
//
// Both recompute passes hit the same shape: iterate (bucket, group,
// provider, model) rows, price each, and set aside the ones with no rate
// card. Keeping the bookkeeping here means neither pass can quietly go
// back to dropping them.
type gapTracker[K comparable] struct {
	events  map[K]int64
	reasons map[K][]string
	seen    map[K]map[string]bool
}

func newGapTracker[K comparable]() *gapTracker[K] {
	return &gapTracker[K]{
		events:  map[K]int64{},
		reasons: map[K][]string{},
		seen:    map[K]map[string]bool{},
	}
}

// add records that n events under k could not be priced because of
// reason. Repeating a reason for the same key is free.
func (g *gapTracker[K]) add(k K, n int64, reason string) {
	g.events[k] += n
	if g.seen[k] == nil {
		g.seen[k] = map[string]bool{}
	}
	if g.seen[k][reason] {
		return
	}
	g.seen[k][reason] = true
	g.reasons[k] = append(g.reasons[k], reason)
}

// at returns the excluded count and reasons for one key.
func (g *gapTracker[K]) at(k K) (int64, []string) {
	return g.events[k], g.reasons[k]
}

// unpricedReason is the machine-greppable form a dashboard groups by.
func unpricedReason(provider, model string) string {
	return "unpriced_model:" + provider + "/" + model
}

// mergeReasons concatenates two reason lists without duplicates,
// preserving order. The equivalent value inherits the metered
// recompute's gaps as well as its own, and the same model commonly
// appears in both.
func mergeReasons(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, r := range list {
			if seen[r] {
				continue
			}
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
