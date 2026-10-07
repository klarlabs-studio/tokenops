package scorecard

import (
	"context"
	"math"
	"time"
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
	// BaselineRef is the operator-supplied baseline identifier carried
	// through to Scorecard.BaselineRef.
	BaselineRef string
	// ClockNow allows tests to inject a deterministic clock. Defaults to
	// time.Now when nil.
	ClockNow func() time.Time
}

// Build grades the live KPIs reader yields, after the operator's
// overrides. A nil reader means there is no event store yet; with no
// overrides and no agent KPIs either, the scorecard reports warming up.
// A failed read is not fatal: the KPIs it would have measured stay
// unmeasured and grade N/A.
func Build(ctx context.Context, reader EventReader, params BuildParams) *Scorecard {
	if params.SinceDays == 0 {
		params.SinceDays = 7
	}
	if params.ClockNow == nil {
		params.ClockNow = time.Now
	}
	// NaN = not measured. Starting from the package defaults here is
	// what let an uncomputed TEU render as a graded 15% B.
	fvt, teu, sac := math.NaN(), math.NaN(), math.NaN()
	agent := params.AgentKPIs
	var anyComputed bool
	if reader != nil {
		since := params.ClockNow().Add(-time.Duration(params.SinceDays) * 24 * time.Hour)
		if kpis, err := Compute(ctx, reader, since); err == nil {
			if kpis.FVTComputed {
				fvt = kpis.FVTSeconds
				anyComputed = true
			}
			if kpis.TEUComputed {
				teu = kpis.TokenEfficiency
				anyComputed = true
			}
			if kpis.SACComputed {
				sac = kpis.SpendAttribution
				anyComputed = true
			}
			if kpis.CHRComputed && !agent.CacheHitRatioComputed {
				agent.CacheHitRatioPct = kpis.CacheHitRatio
				agent.CacheHitRatioComputed = true
				anyComputed = true
			}
		}
	}
	if params.FVTSecondsOverride > 0 {
		fvt = params.FVTSecondsOverride
		anyComputed = true
	}
	if params.TEUPctOverride > 0 {
		teu = params.TEUPctOverride
		anyComputed = true
	}
	if params.SACPctOverride > 0 {
		sac = params.SACPctOverride
		anyComputed = true
	}
	if agent.CacheHitRatioComputed || agent.ConfirmationGateComputed || agent.RegenerateComputed ||
		agent.ToolSuccessComputed || agent.DestructiveComputed {
		anyComputed = true
	}
	if !anyComputed {
		return NewWarmingUp(params.BaselineRef)
	}
	return NewWithAgentKPIs(fvt, teu, sac, agent, params.BaselineRef)
}
