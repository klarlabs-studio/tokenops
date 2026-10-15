package scorecard

import (
	"context"
	"math"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Reference values for the wedge KPIs, retained for documentation and
// for operators calibrating their own targets.
//
// These are deliberately NOT fallbacks. Substituting one for a metric
// the store could not compute is how an unmeasured TEU came to print as
// a graded "15.0 [B]" — an invention rendered indistinguishably from an
// observation. An unmeasured KPI now stays NaN and reports N/A.
const (
	DefaultFVTSeconds = 45.0
	DefaultTEUPct     = 15.0
	DefaultSACPct     = 80.0
)

// BuildParams bundles every input the adapters supply when constructing
// the wedge scorecard.
type BuildParams struct {
	// SinceDays bounds the live compute window. Zero defaults to 7.
	SinceDays int
	// Overrides, when non-zero, replace the corresponding live KPI value.
	FVTSecondsOverride float64
	TEUPctOverride     float64
	SACPctOverride     float64
	// AgentKPIs supplies the agent-workflow metrics the event store
	// cannot: CGR, RGR, TCS and DAR come from session transcripts. CHR
	// can be passed here OR derived from the events via computeCHR;
	// the caller's figure wins.
	AgentKPIs AgentKPIInputs
	// AgentKPIBaseline is the same agent-workflow metrics over the
	// baseline window (the BaselineDays before the period), which each
	// KPI is set against. CHR falls back to the event store's, as above.
	AgentKPIBaseline AgentKPIInputs
	// BaselineRef is the operator-supplied baseline identifier carried
	// through to Scorecard.BaselineRef.
	BaselineRef string
	// UnitCosts are the period's unit costs, measured by the caller (they
	// join agent work to git history, which this domain does not read).
	UnitCosts []UnitCost
	// Allocation is the share of spend the allocation rules tag, measured
	// by the caller (it joins sessions to where they ran).
	Allocation *AllocationCoverage
	// ClockNow allows tests to inject a deterministic clock. Defaults to
	// time.Now when nil.
	ClockNow func() time.Time
}

// Build grades the live KPIs reader yields, after the operator's
// overrides, and sets each against the operator's own baseline: the
// BaselineDays before the period. A nil reader means there is no event
// store yet; with no overrides and no agent KPIs either, the scorecard
// reports warming up. A failed read is not fatal: the KPIs it would have
// measured stay unmeasured and grade N/A.
func Build(ctx context.Context, reader EventReader, params BuildParams) *Scorecard {
	if params.SinceDays == 0 {
		params.SinceDays = 7
	}
	if params.ClockNow == nil {
		params.ClockNow = time.Now
	}
	now := params.ClockNow()
	since := now.Add(-time.Duration(params.SinceDays) * 24 * time.Hour)
	baselineSince := since.AddDate(0, 0, -BaselineDays)
	// NaN = not measured. Starting from the package defaults here is
	// what let an uncomputed TEU render as a graded 15% B.
	fvt, teu, sac := math.NaN(), math.NaN(), math.NaN()
	agent, agentBase := params.AgentKPIs, params.AgentKPIBaseline
	var anyComputed bool
	var planUse, basePlanUse *plans.PlanUseReport
	base := baselineValues{fvt: math.NaN(), teu: math.NaN(), sac: math.NaN()}
	if reader != nil {
		if kpis, baseKPIs, err := ComputeWithBaseline(ctx, reader, baselineSince, since, now); err == nil {
			if len(kpis.PlanUse.Providers) > 0 {
				planUse = &kpis.PlanUse
			}
			if len(baseKPIs.PlanUse.Providers) > 0 {
				basePlanUse = &baseKPIs.PlanUse
			}
			fvt, teu, sac, anyComputed = measured(kpis)
			base.fvt, base.teu, base.sac, _ = measured(baseKPIs)
			if kpis.CHRComputed && !agent.CacheHitRatioComputed {
				agent.CacheHitRatioPct, agent.CacheHitRatioComputed = kpis.CacheHitRatio, true
				anyComputed = true
			}
			if baseKPIs.CHRComputed && !agentBase.CacheHitRatioComputed {
				agentBase.CacheHitRatioPct, agentBase.CacheHitRatioComputed = baseKPIs.CacheHitRatio, true
			}
		}
	}
	// An operator's own figure replaces the measurement; there is nothing
	// of theirs to set it against.
	if params.FVTSecondsOverride > 0 {
		fvt, base.fvt, anyComputed = params.FVTSecondsOverride, math.NaN(), true
	}
	if params.TEUPctOverride > 0 {
		teu, base.teu, anyComputed = params.TEUPctOverride, math.NaN(), true
	}
	if params.SACPctOverride > 0 {
		sac, base.sac, anyComputed = params.SACPctOverride, math.NaN(), true
	}
	if agent.CacheHitRatioComputed || agent.ConfirmationGateComputed || agent.RegenerateComputed ||
		agent.ToolSuccessComputed || agent.DestructiveComputed {
		anyComputed = true
	}
	if !anyComputed {
		return NewWarmingUp(params.BaselineRef)
	}
	sc := NewWithAgentKPIs(fvt, teu, sac, agent, params.BaselineRef)
	sc.PlanUse, sc.PlanUseDays = planUse, params.SinceDays
	sc.UnitCosts = withUnitCostDirections(params.UnitCosts)
	base.chr, base.cgr, base.rgr, base.tcs, base.dar = agentValues(agentBase)
	compared := sc.applyBaseline(base)
	sc.PlanUseVsBaseline = comparePlanUse(planUse, basePlanUse)
	if compared || len(sc.PlanUseVsBaseline) > 0 {
		sc.SelfBaseline = &SelfBaseline{Since: baselineSince, Until: since, Days: BaselineDays}
	}
	sc.Allocation = params.Allocation
	return sc
}

// measured is the store KPIs a window yielded, NaN where it measured
// none, and whether it measured any.
func measured(k *LiveKPIs) (fvt, teu, sac float64, ok bool) {
	fvt, teu, sac = math.NaN(), math.NaN(), math.NaN()
	if k.FVTComputed {
		fvt, ok = k.FVTSeconds, true
	}
	if k.TEUComputed {
		teu, ok = k.TokenEfficiency, true
	}
	if k.SACComputed {
		sac, ok = k.SpendAttribution, true
	}
	return fvt, teu, sac, ok
}

// agentValues is the agent-workflow KPIs, NaN where not computed.
func agentValues(a AgentKPIInputs) (chr, cgr, rgr, tcs, dar float64) {
	pick := func(v float64, ok bool) float64 {
		if !ok {
			return math.NaN()
		}
		return v
	}
	return pick(a.CacheHitRatioPct, a.CacheHitRatioComputed), pick(a.ConfirmationGateRatePct, a.ConfirmationGateComputed),
		pick(a.RegenerateRatePct, a.RegenerateComputed), pick(a.ToolSuccessRatePct, a.ToolSuccessComputed),
		pick(a.DestructiveRatePct, a.DestructiveComputed)
}

// withUnitCostDirections reads each unit cost's change against its
// baseline, in a new slice.
func withUnitCostDirections(us []UnitCost) []UnitCost {
	if us == nil {
		return nil
	}
	out := make([]UnitCost, len(us))
	for i, u := range us {
		u.Direction = unitCostDirection(u.ChangePct)
		out[i] = u
	}
	return out
}
