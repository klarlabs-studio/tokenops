package spending

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// scorecardDefaultDays is the period a scorecard grades when none is
// asked for.
const scorecardDefaultDays = 7

// ScorecardReach is how far back a scorecard of the default period reads:
// the period and the baseline it is compared with. A cache of events
// meant to serve it holds at least this.
const ScorecardReach = (scorecardDefaultDays + scorecard.BaselineDays) * 24 * time.Hour

// ScorecardParams selects the scorecard's window and operator overrides.
type ScorecardParams struct {
	// SinceDays is the window; seven when zero.
	SinceDays int
	// The overrides replace a measured KPI with the operator's figure.
	FVTSeconds, TEUPct, SACPct float64
	BaselineRef                string
	// TranscriptRoot is where the agent KPIs read session transcripts;
	// empty means the harnesses' default locations.
	TranscriptRoot string
	// Now anchors the window; the current time when zero.
	Now time.Time
	// Spend prices the turns the unit costs are measured from; nil leaves
	// unit costs off the scorecard.
	Spend *Engine
	// Allocation are the allocation rules; with Spend, the scorecard
	// shows how much of the period's spend they tag.
	Allocation []allocation.Rule
	// Events is what the wedge KPIs read the period's events through;
	// the store when nil. A process that outlives one scorecard passes a
	// cache of them, so each scorecard does not decode five weeks again.
	Events scorecard.EventReader
	// AgentFacts is what earlier scorecards read of the transcripts, so
	// this one reads only what was written since; nil reads them all.
	AgentFacts *AgentFacts
}

// ScorecardReport is the scorecard answer, named here so adapters can
// describe it without reaching into the governance domain.
type ScorecardReport = scorecard.Scorecard

// Scorecard grades the wedge KPIs (FVT, TEU, SAC, CHR) from the store and
// the agent KPIs (CGR, RGR, TCS, DAR) from session transcripts. A nil
// store means there is no event history yet.
//
// The CLI used to be the only surface that added the agent KPIs, so the
// MCP tool and /api/scorecard graded the same week on four fewer
// metrics. Every surface now asks this function.
//
// It reads the period and the four weeks before it. What that costs is
// the caller's to keep: p.Events and p.AgentFacts hold the events and the
// transcripts between two scorecards.
func Scorecard(ctx context.Context, store *sqlite.Store, p ScorecardParams) *ScorecardReport {
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	days := p.SinceDays
	if days <= 0 {
		days = scorecardDefaultDays
	}
	reader := p.Events
	var (
		units []scorecard.UnitCost
		alloc *scorecard.AllocationCoverage
	)
	if store != nil {
		if reader == nil {
			reader = store
		}
		if p.Spend != nil {
			agg := NewAggregator(store, p.Spend)
			since := now.Add(-time.Duration(days) * 24 * time.Hour)
			r, err := uniteconomics.Compute(ctx, uniteconomics.Deps{Turns: commits.TurnsIn(agg, Window{}), Events: store},
				since, now)
			if err == nil {
				units = scorecardUnitCosts(r)
			}
			alloc = allocationCoverage(ctx, agg, p.Allocation, since, now)
		}
	}
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	facts, factsCtx := p.AgentFacts, ctx
	if facts == nil {
		facts = &AgentFacts{}
	} else {
		// What is read is kept for the next scorecard, so a caller that
		// stopped waiting does not stop the read: a first read cut short
		// would start again from nothing each time it was asked for.
		factsCtx = context.WithoutCancel(ctx)
	}
	agent, agentBase := facts.kpis(factsCtx, p.TranscriptRoot, since.AddDate(0, 0, -scorecard.BaselineDays), since, now)
	return scorecard.Build(ctx, reader, scorecard.BuildParams{
		UnitCosts:          units,
		Allocation:         alloc,
		SinceDays:          days,
		FVTSecondsOverride: p.FVTSeconds,
		TEUPctOverride:     p.TEUPct,
		SACPctOverride:     p.SACPct,
		AgentKPIs:          agent,
		AgentKPIBaseline:   agentBase,
		BaselineRef:        p.BaselineRef,
		ClockNow:           func() time.Time { return now },
	})
}

// scorecardUnitCosts are the headline unit costs the scorecard shows: per
// commit, per merged pull request and per achieved outcome.
func scorecardUnitCosts(r uniteconomics.Report) []scorecard.UnitCost {
	out := make([]scorecard.UnitCost, 0, 3)
	for _, u := range []struct {
		label string
		unit  uniteconomics.Unit
	}{{"commit", r.Commits}, {"merged PR", r.PRs}, {"achieved outcome", r.Achieved}} {
		c := scorecard.UnitCost{Unit: u.label, Units: u.unit.Current.Units, ChangePct: u.unit.ChangePct,
			BaselinePerUnitUSD: u.unit.Baseline.PerUnitUSD}
		if v := u.unit.Current.PerUnitUSD; v != nil {
			c.PerUnitUSD = *v
		}
		out = append(out, c)
	}
	return out
}

// allocationCoverage is how much of [since, now)'s spend the rules tag;
// nil without rules or when the turns cannot be read.
func allocationCoverage(ctx context.Context, agg *EventAggregator, rules []allocation.Rule, since, now time.Time) *scorecard.AllocationCoverage {
	if len(rules) == 0 {
		return nil
	}
	turns, err := allocation.Tagged(ctx, allocation.Deps{Turns: commits.TurnsIn(agg, Window{}), Rules: rules}, since, now)
	if err != nil {
		return nil
	}
	c := allocation.CoverageOf(len(rules), turns)
	return &scorecard.AllocationCoverage{Rules: c.Rules, TaggedPct: c.TaggedPct, ByKeyPct: c.ByKeyPct}
}
