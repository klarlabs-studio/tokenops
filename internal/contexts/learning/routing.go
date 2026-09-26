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
	Metrics           map[string]MetricPair
	PolicyViolation   bool
	At                time.Time
	Fingerprint       string
}

// MetricPair preserves the independently measured value for each arm. Missing
// values are absent from the map rather than represented as zero.
type MetricPair struct {
	Baseline float64
	Variant  float64
}

// Guardrail declares a maximum permitted relative regression. Quality is the
// special strict non-inferiority guardrail and requires strong outcomes.
type Guardrail struct {
	Metric           string
	MaxRegressionPct float64
}

// Evidence is the locally accumulated history for one routing context.
type Evidence struct {
	Eligible          int
	Pairs             []Pair
	Fingerprint       string
	ObjectiveMetric   string
	MinImprovementPct float64
	Guardrails        []Guardrail
	Now               time.Time
	Window            time.Duration
}

// Belief is a reproducible reading of Evidence.
type Belief struct {
	Tier                  Tier    `json:"tier"`
	CompletedPairs        int     `json:"completed_pairs"`
	StrongCoverage        float64 `json:"strong_coverage"`
	QualitySafePairs      int     `json:"quality_safe_pairs"`
	ImprovedPairs         int     `json:"improved_pairs"`
	MedianChangePct       float64 `json:"median_change_pct"`
	GuardrailPassPairs    int     `json:"guardrail_pass_pairs"`
	GuardrailUnknownPairs int     `json:"guardrail_unknown_pairs"`
	ObjectiveUnknownPairs int     `json:"objective_unknown_pairs"`
	ObjectiveMetric       string  `json:"objective_metric,omitempty"`
	Caveat                string  `json:"caveat,omitempty"`
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
	b.ObjectiveMetric = e.ObjectiveMetric
	if e.Eligible > 0 {
		b.StrongCoverage = float64(len(pairs)*2) / float64(e.Eligible)
	}
	var changes []float64
	for _, p := range pairs {
		if qualityRank(p.Variant.Result) >= qualityRank(p.Baseline.Result) {
			b.QualitySafePairs++
		}
		metric, objectiveKnown := p.Metrics[e.ObjectiveMetric]
		if objectiveKnown {
			change := relativeImprovement(metric.Baseline, metric.Variant)
			changes = append(changes, change)
			if change >= e.MinImprovementPct {
				b.ImprovedPairs++
			}
		} else {
			b.ObjectiveUnknownPairs++
		}
		known, passed := guardrailsPass(p, e.Guardrails)
		if !known {
			b.GuardrailUnknownPairs++
		} else if passed {
			b.GuardrailPassPairs++
		}
	}
	b.MedianChangePct = median(changes)
	if len(pairs) == 0 {
		b.Tier, b.Caveat = TierUnknown, "no fresh matched pairs with independent or human outcomes"
		return b
	}
	b.Tier = TierObserved
	if !validUtilityPolicy(e.ObjectiveMetric, e.MinImprovementPct, e.Guardrails) {
		b.Caveat = "explicit objective and quality/resource guardrails are required; legacy trials remain observational"
		return b
	}
	if len(pairs) < 5 || b.StrongCoverage < .6 {
		b.Caveat = "at least five matched pairs and 60% strong outcome coverage are required"
		return b
	}
	if b.GuardrailUnknownPairs > 0 {
		b.Caveat = "unknown guardrail evidence blocks promotion"
		return b
	}
	if b.ObjectiveUnknownPairs > 0 {
		b.Caveat = "unknown objective measurements block promotion"
		return b
	}
	if b.GuardrailPassPairs != len(pairs) {
		b.Caveat = "at least one pair violated a declared guardrail"
		return b
	}
	if float64(b.QualitySafePairs)/float64(len(pairs)) < .8 ||
		float64(b.ImprovedPairs)/float64(len(pairs)) < .6 || b.MedianChangePct < e.MinImprovementPct {
		b.Caveat = "quality non-inferiority or declared objective-improvement gates were not met"
		return b
	}
	b.Tier = TierSupported
	if len(pairs) >= 20 && b.StrongCoverage >= .9 {
		b.Tier = TierTrusted
	}
	return b
}

func validUtilityPolicy(objective string, minImprovementPct float64, guardrails []Guardrail) bool {
	if objective != "tokens" && objective != "plan_quota_tokens" && objective != "metered_cost_usd" && objective != "latency_ms" && objective != "attention_minutes" {
		return false
	}
	if minImprovementPct <= 0 || minImprovementPct > 100 || len(guardrails) == 0 {
		return false
	}
	quality := false
	seen := map[string]bool{}
	for _, g := range guardrails {
		if seen[g.Metric] {
			return false
		}
		seen[g.Metric] = true
		if g.Metric == "quality" {
			if g.MaxRegressionPct != 0 {
				return false
			}
			quality = true
			continue
		}
		if g.Metric != "tokens" && g.Metric != "plan_quota_tokens" && g.Metric != "metered_cost_usd" && g.Metric != "latency_ms" && g.Metric != "attention_minutes" {
			return false
		}
		if g.MaxRegressionPct < 0 || g.MaxRegressionPct > 100 {
			return false
		}
	}
	return quality
}

func guardrailsPass(p Pair, guardrails []Guardrail) (known, passed bool) {
	if len(guardrails) == 0 {
		return false, false
	}
	for _, g := range guardrails {
		if g.Metric == "quality" {
			if !strong(p.Baseline) || !strong(p.Variant) {
				return false, false
			}
			if qualityRank(p.Variant.Result) < qualityRank(p.Baseline.Result) {
				return true, false
			}
			continue
		}
		metric, ok := p.Metrics[g.Metric]
		if !ok {
			return false, false
		}
		if relativeRegression(metric.Baseline, metric.Variant) > g.MaxRegressionPct {
			return true, false
		}
	}
	return true, true
}

func relativeRegression(baseline, variant float64) float64 {
	if baseline <= 0 {
		if variant <= 0 {
			return 0
		}
		return 100
	}
	return 100 * (variant - baseline) / baseline
}

func relativeImprovement(baseline, variant float64) float64 {
	if baseline <= 0 {
		if variant <= 0 {
			return 0
		}
		return -100
	}
	return 100 * (baseline - variant) / baseline
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
