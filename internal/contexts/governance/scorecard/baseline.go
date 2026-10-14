package scorecard

import (
	"fmt"
	"math"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// BaselineDays is the operator's own trailing baseline: the four weeks
// before the scorecard's period. It is the same window unit costs are
// set against, so every comparison on one scorecard reads against one
// stretch of the operator's own history.
const BaselineDays = 28

// Polarity is which way a figure is healthy.
type Polarity string

const (
	HigherIsBetter Polarity = "higher_is_better"
	LowerIsBetter  Polarity = "lower_is_better"
	// Neutral figures are reported without a verdict (plan use): a seat
	// used more is a fact for whoever pays for it, not an improvement.
	Neutral Polarity = "neutral"
)

// Direction is how a figure moved against the baseline. Better and worse
// apply the figure's polarity; up and down are for neutral figures.
type Direction string

const (
	DirectionBetter    Direction = "better"
	DirectionWorse     Direction = "worse"
	DirectionUnchanged Direction = "unchanged"
	DirectionUp        Direction = "up"
	DirectionDown      Direction = "down"
)

// Comparison is a figure set against the operator's own baseline.
type Comparison struct {
	// Baseline is the figure over the baseline window, in the figure's unit.
	Baseline float64 `json:"baseline"`
	// Delta is the period's figure minus Baseline.
	Delta     float64   `json:"delta"`
	Direction Direction `json:"direction"`
}

// SelfBaseline is the window the scorecard's comparisons read against.
type SelfBaseline struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	Days  int       `json:"days"`
}

// PlanUseComparison is one provider's plan use against the baseline.
// Both figures are neutral.
type PlanUseComparison struct {
	Provider    string      `json:"provider"`
	Utilization *Comparison `json:"utilization,omitempty"`
	Coverage    *Comparison `json:"coverage,omitempty"`
}

// compare sets current against baseline. A move smaller than tolerance,
// in the figure's unit, is unchanged: noise is not a trend.
func compare(current, baseline float64, p Polarity, tolerance float64) Comparison {
	delta := current - baseline
	c := Comparison{Baseline: baseline, Delta: math.Round(delta*100) / 100}
	switch {
	case math.Abs(delta) < tolerance:
		c.Direction = DirectionUnchanged
	case p == Neutral && delta > 0:
		c.Direction = DirectionUp
	case p == Neutral:
		c.Direction = DirectionDown
	case (delta > 0) == (p == HigherIsBetter):
		c.Direction = DirectionBetter
	default:
		c.Direction = DirectionWorse
	}
	return c
}

// Tolerances: a percentage KPI must move a full point to count, the
// destructive-action rate (healthy at 0, graded in tenths) a tenth of
// one, first-value time a second or 5%, and a unit cost 5%.
const (
	pctTolerance       = 1.0
	darTolerance       = 0.1
	fvtMinTolerance    = 1.0
	relativeTolerance  = 0.05
	unitCostTolerance  = 5.0 // percent change
	kpiValueDecimals   = 1
	darValueDecimals   = 2
	planPctTolerance   = pctTolerance
	baselineHeaderDate = "Jan 2"
)

// baselineValues are the KPIs over the baseline window. NaN = not
// measured there.
type baselineValues struct {
	fvt, teu, sac, chr, cgr, rgr, tcs, dar float64
}

func roundTo(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

// withBaseline attaches a comparison to k when both it and the baseline
// were measured.
func withBaseline(k *KPIResult, base float64, decimals int, tolerance float64) bool {
	if k.Grade == "" || math.IsNaN(base) || math.IsNaN(k.Value) {
		return false
	}
	c := compare(k.Value, roundTo(base, decimals), k.Polarity, tolerance)
	k.VsBaseline = &c
	return true
}

// applyBaseline sets every measured KPI against the baseline and reports
// whether any comparison was made.
func (s *Scorecard) applyBaseline(b baselineValues) bool {
	compared := false
	fvtTol := math.Max(fvtMinTolerance, relativeTolerance*b.fvt)
	for _, c := range []struct {
		k    *KPIResult
		base float64
		dec  int
		tol  float64
	}{
		{&s.FirstValueTime, b.fvt, kpiValueDecimals, fvtTol},
		{&s.TokenEfficiency, b.teu, kpiValueDecimals, pctTolerance},
		{&s.SpendAttribution, b.sac, kpiValueDecimals, pctTolerance},
		{&s.CacheHitRatio, b.chr, kpiValueDecimals, pctTolerance},
		{&s.ConfirmationGateRate, b.cgr, kpiValueDecimals, pctTolerance},
		{&s.RegenerateRate, b.rgr, kpiValueDecimals, pctTolerance},
		{&s.ToolSuccessRate, b.tcs, kpiValueDecimals, pctTolerance},
		{&s.DestructiveRate, b.dar, darValueDecimals, darTolerance},
	} {
		if withBaseline(c.k, c.base, c.dec, c.tol) {
			compared = true
		}
	}
	return compared
}

// comparePlanUse sets each provider's plan use against the baseline's.
func comparePlanUse(cur, base *plans.PlanUseReport) []PlanUseComparison {
	if cur == nil || base == nil {
		return nil
	}
	var out []PlanUseComparison
	for _, p := range cur.Providers {
		b, ok := base.For(p.Provider)
		if !ok {
			continue
		}
		c := PlanUseComparison{Provider: p.Provider}
		if p.UtilizationPct != nil && b.UtilizationPct != nil && p.UtilizationWindow == b.UtilizationWindow {
			v := compare(*p.UtilizationPct, *b.UtilizationPct, Neutral, planPctTolerance)
			c.Utilization = &v
		}
		if p.CoveragePct != nil && b.CoveragePct != nil {
			v := compare(*p.CoveragePct, *b.CoveragePct, Neutral, planPctTolerance)
			c.Coverage = &v
		}
		if c.Utilization != nil || c.Coverage != nil {
			out = append(out, c)
		}
	}
	return out
}

// unitCostDirection reads a unit cost's change against its baseline:
// cheaper finished work is better.
func unitCostDirection(changePct *float64) Direction {
	if changePct == nil {
		return ""
	}
	switch {
	case math.Abs(*changePct) < unitCostTolerance:
		return DirectionUnchanged
	case *changePct < 0:
		return DirectionBetter
	default:
		return DirectionWorse
	}
}

// renderVsBaseline is the " · baseline X (+d, better)" suffix of a KPI
// line, or "" when the KPI has no comparison.
func renderVsBaseline(k KPIResult, decimals int) string {
	c := k.VsBaseline
	if c == nil {
		return ""
	}
	return fmt.Sprintf(" · baseline %.*f (%+.*f, %s)", decimals, c.Baseline, decimals, c.Delta, c.Direction)
}

// renderPlanVs is the " (baseline 52%, up)" suffix of a plan-use figure.
func renderPlanVs(c *Comparison) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf(" (baseline %.0f%%, %s)", c.Baseline, c.Direction)
}
