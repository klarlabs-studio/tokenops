// Package learning derives retractable beliefs from outcome-linked evidence.
// Beliefs are projections; the append-only events remain the authority.
package learning

import (
	"sort"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Tier says how much behavior a route belief may influence.
type Tier string

const (
	TierUnknown   Tier = "unknown"
	TierObserved  Tier = "observed"
	TierSupported Tier = "supported"
	TierTrusted   Tier = "trusted"
)

// Pair compares matched baseline and routed executions.
type Pair struct {
	Baseline          eventschema.OutcomeEvent
	Variant           eventschema.OutcomeEvent
	ResourceChangePct float64
	PolicyViolation   bool
	At                time.Time
	Fingerprint       string
}

// Evidence is the locally accumulated history for one routing context.
type Evidence struct {
	Eligible    int
	Pairs       []Pair
	Fingerprint string
	Now         time.Time
	Window      time.Duration
}

// Belief is a reproducible reading of Evidence.
type Belief struct {
	Tier             Tier    `json:"tier"`
	CompletedPairs   int     `json:"completed_pairs"`
	StrongCoverage   float64 `json:"strong_coverage"`
	QualitySafePairs int     `json:"quality_safe_pairs"`
	ImprovedPairs    int     `json:"improved_pairs"`
	MedianChangePct  float64 `json:"median_change_pct"`
	Caveat           string  `json:"caveat,omitempty"`
}

// Evaluate applies the evidence gates. Stale or environment-mismatched pairs
// stay in history but cannot promote current behavior.
func Evaluate(e Evidence) Belief {
	now := e.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	window := e.Window
	if window <= 0 {
		window = 90 * 24 * time.Hour
	}
	var pairs []Pair
	for _, p := range e.Pairs {
		if p.Fingerprint != e.Fingerprint || p.At.Before(now.Add(-window)) || !strong(p.Baseline) || !strong(p.Variant) {
			continue
		}
		pairs = append(pairs, p)
	}
	b := Belief{CompletedPairs: len(pairs)}
	if e.Eligible > 0 {
		b.StrongCoverage = float64(len(pairs)*2) / float64(e.Eligible)
	}
	var changes []float64
	for _, p := range pairs {
		if p.PolicyViolation {
			b.Caveat = "at least one variant violated policy"
			continue
		}
		if qualityRank(p.Variant.Result) >= qualityRank(p.Baseline.Result) {
			b.QualitySafePairs++
		}
		if p.ResourceChangePct >= 10 {
			b.ImprovedPairs++
		}
		changes = append(changes, p.ResourceChangePct)
	}
	b.MedianChangePct = median(changes)
	if len(pairs) == 0 {
		b.Tier, b.Caveat = TierUnknown, "no fresh matched pairs with independent or human outcomes"
		return b
	}
	b.Tier = TierObserved
	if len(pairs) < 5 || b.StrongCoverage < .6 {
		b.Caveat = "at least five matched pairs and 60% strong outcome coverage are required"
		return b
	}
	if b.Caveat != "" || float64(b.QualitySafePairs)/float64(len(pairs)) < .8 ||
		float64(b.ImprovedPairs)/float64(len(pairs)) < .6 || b.MedianChangePct < 10 {
		b.Caveat = "quality non-inferiority or resource-improvement gates were not met"
		return b
	}
	b.Tier = TierSupported
	if len(pairs) >= 20 && b.StrongCoverage >= .9 {
		b.Tier = TierTrusted
	}
	return b
}

func strong(o eventschema.OutcomeEvent) bool {
	return (o.Assessment == eventschema.OutcomeVerification || o.Assessment == eventschema.OutcomeHuman) &&
		o.Result != eventschema.OutcomeUnknown
}

func qualityRank(r eventschema.OutcomeResult) int {
	switch r {
	case eventschema.OutcomeAchieved:
		return 3
	case eventschema.OutcomePartial:
		return 2
	case eventschema.OutcomeNotAchieved:
		return 1
	default:
		return 0
	}
}

func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	v := append([]float64(nil), vs...)
	sort.Float64s(v)
	mid := len(v) / 2
	if len(v)%2 == 0 {
		return (v[mid-1] + v[mid]) / 2
	}
	return v[mid]
}
