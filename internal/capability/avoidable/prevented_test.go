package avoidable

import (
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/effortweeks"
	"go.klarlabs.de/tokenops/internal/capability/explain"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/learning/effortwatch"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// withPrevented is the fixture plus one of each mechanism the prevented
// figures read: two fmt compressions, a paused and a denied boundary call,
// a loop-breaker stop, and a routing move the holdout measured at $1.50.
func withPrevented() Inputs {
	in := fixture()
	in.Fmt = []FmtSaving{{At: t0.Add(day), Tokens: 1_000}, {At: t0.Add(2 * day), Tokens: 2_000}}
	in.Holds = []Hold{
		{At: t0.Add(day), Mechanism: MechBoundary, Denied: false},
		{At: t0.Add(day), Mechanism: MechBoundary, Denied: true},
		{At: t0.Add(2 * day), Mechanism: MechLoopBreaker},
	}
	in.Savings.Kinds = []learning.KindSavings{{Capability: learning.CapabilityRouting, Kind: "lookup",
		Treated: learning.CohortStats{Count: 6}, RealizedUSD: measurement.Estimated(1.5, "randomized holdout", "")}}
	in.Savings.RealizedUSD = measurement.Estimated(1.5, "randomized holdout", "")
	return in
}

func mechanism(r Report, key string) (Mechanism, bool) {
	for _, m := range r.Prevented.Mechanisms {
		if m.Key == key {
			return m, true
		}
	}
	return Mechanism{}, false
}

func TestPreventedCountsEachMechanism(t *testing.T) {
	r := Build(withPrevented())
	tests := []struct {
		key    string
		count  int
		tokens int64
		usd    float64
		priced bool
	}{
		// 500 refused tokens at the kept session's $1 per 1,000.
		{MechReadGuard, 1, 500, 0.5, true},
		// Two fmt compressions, 3,000 tokens at the period's average rate.
		{MechFmt, 2, 3_000, 3_000 * (r.Usage.APIEquivalentUSD / float64(r.Usage.Tokens)), true},
		// One paused and one denied call: both are holds.
		{MechBoundary, 2, 0, 0, false},
		{MechLoopBreaker, 1, 0, 0, false},
		{MechAutopilot, 6, 0, 1.5, true},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			m, ok := mechanism(r, tt.key)
			if !ok {
				t.Fatalf("no %s mechanism in %+v", tt.key, r.Prevented.Mechanisms)
			}
			if m.Count != tt.count || m.Tokens != tt.tokens {
				t.Errorf("count, tokens = %d, %d; want %d, %d", m.Count, m.Tokens, tt.count, tt.tokens)
			}
			v, ok := m.USD.Amount()
			if ok != tt.priced {
				t.Fatalf("priced = %v, want %v (%s)", ok, tt.priced, m.USD.Caveat())
			}
			if ok && math.Abs(v-tt.usd) > 1e-9 {
				t.Errorf("usd = %v, want %v", v, tt.usd)
			}
			if m.Basis == "" {
				t.Errorf("%s carries no basis", tt.key)
			}
		})
	}
}

// Prevented is never added into found: the avoidable dollars and classes
// are the same with or without the prevented inputs.
func TestPreventedIsNeverAddedIntoFound(t *testing.T) {
	base := Build(fixture())
	with := Build(withPrevented())
	if math.Abs(base.Avoidable.APIEquivalentUSD-with.Avoidable.APIEquivalentUSD) > 1e-9 || base.Avoidable.Tokens != with.Avoidable.Tokens {
		t.Errorf("avoidable moved with prevented inputs: %+v vs %+v", base.Avoidable, with.Avoidable)
	}
	if with.Prevented.USD <= 0 {
		t.Errorf("prevented usd = %v, want the sum of the priced mechanisms", with.Prevented.USD)
	}
}

func TestPreventedUnpricedUntilTheHoldoutConcludes(t *testing.T) {
	in := withPrevented()
	in.Savings.Kinds[0].RealizedUSD = measurement.Unknown("not established: no intervention of this kind was held back")
	m, ok := mechanism(Build(in), MechAutopilot)
	if !ok {
		t.Fatal("the autopilot mechanism is missing while its holdout is open")
	}
	if _, priced := m.USD.Amount(); priced {
		t.Errorf("autopilot priced before the holdout concluded: %v", m.USD)
	}
}

func TestPreventedWithNothingRecorded(t *testing.T) {
	r := Build(Inputs{Since: t0, Until: t0.Add(day), Savings: savings.Report{RealizedUSD: measurement.Unknown("none")}})
	if r.Prevented.USD != 0 {
		t.Errorf("prevented usd = %v with nothing recorded", r.Prevented.USD)
	}
	for _, m := range r.Prevented.Mechanisms {
		if m.Count != 0 {
			t.Errorf("%s counted %d with nothing recorded", m.Key, m.Count)
		}
	}
}

func TestHoldClassifiesBoundaryDecisions(t *testing.T) {
	loop := boundaryPolicy(config.LoopBreakerName)
	other := boundaryPolicy("no-models-after-noon")
	tests := []struct {
		name     string
		policy   string
		kind     string
		stage    eventschema.DecisionStage
		approver string
		want     string
	}{
		{"paused by an unapproved boundary", other, "boundary", eventschema.DecisionStageApplied, "", MechBoundary},
		{"denied by the operator", other, "boundary", eventschema.DecisionStageRejected, "", MechBoundary},
		{"approved is not held", other, "boundary", eventschema.DecisionStageApplied, "alice", ""},
		{"shadow only would have held", other, "boundary", eventschema.DecisionStageShadow, "", ""},
		{"loop breaker stop", loop, "boundary", eventschema.DecisionStageApplied, "", MechLoopBreaker},
		{"loop breaker denied", loop, "boundary", eventschema.DecisionStageRejected, "", MechLoopBreaker},
		{"not a boundary decision", other, "route", eventschema.DecisionStageApplied, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &eventschema.Envelope{Type: eventschema.EventTypeDecision, Timestamp: t0, Source: "boundary",
				Payload: &eventschema.DecisionEvent{Kind: tt.kind, Stage: tt.stage, Policy: tt.policy,
					Approval: eventschema.DecisionApproval{Approver: tt.approver}}}
			h, ok := holdOf(env)
			switch {
			case tt.want == "" && ok:
				t.Errorf("classified as %s, want no hold", h.Mechanism)
			case tt.want != "" && (!ok || h.Mechanism != tt.want):
				t.Errorf("hold = %+v, %v; want %s", h, ok, tt.want)
			}
		})
	}
}

func TestFmtReadsOnlyCommandFmtEvents(t *testing.T) {
	fmtEvent := &eventschema.Envelope{Type: eventschema.EventTypeOptimization, Timestamp: t0,
		Payload: &eventschema.OptimizationEvent{Kind: eventschema.OptimizationTypeCommandFmt, EstimatedSavingsTokens: 700}}
	cache := &eventschema.Envelope{Type: eventschema.EventTypeOptimization, Timestamp: t0,
		Payload: &eventschema.OptimizationEvent{Kind: eventschema.OptimizationTypeCacheReuse, EstimatedSavingsTokens: 900}}
	got := fmtSavingsOf([]*eventschema.Envelope{fmtEvent, cache})
	if len(got) != 1 || got[0].Tokens != 700 {
		t.Errorf("fmt savings = %+v, want the one command_fmt event of 700 tokens", got)
	}
}

func boundaryPolicy(rule string) string { return explain.BoundaryPolicyPrefix + rule }

// The headline names what was prevented before what is still found, and
// says a mechanism that has not priced yet rather than stating zero.
func TestHeadlineLeadsWithPreventedThenFound(t *testing.T) {
	r := Build(withPrevented())
	p, f := strings.Index(r.Headline, "prevented ~$"), strings.Index(r.Headline, "still found ~$")
	if p < 0 || f < 0 || p > f {
		t.Errorf("headline = %q, want prevented before still found", r.Headline)
	}

	in := fixture()
	in.Fmt, in.Refused, in.Savings.Kinds = nil, nil, nil
	in.Holds = []Hold{{At: t0.Add(day), Mechanism: MechBoundary}}
	got := Build(in).Headline
	if !strings.Contains(got, "prevented not priced yet") {
		t.Errorf("headline = %q, want the unpriced boundary holds named as not priced", got)
	}
}

// realizedWeeks is a rollup set before the period whose four weeks after
// the setting carry 600,000 fewer tokens above the line each week, with the
// reject rate down too: a realized saving.
func realizedWeeks(setAt time.Time) *Weeks {
	w := &Weeks{SetAt: setAt}
	from := judgedFrom(setAt)
	for i := -4; i < 4; i++ {
		f := WeekFigs{Start: from.AddDate(0, 0, 7*i), OversizedTokens: 900_000 + int64((i+8)%2)*5_000, RejectRatePct: 15, Prompts: 100}
		if i >= 0 {
			f.OversizedTokens, f.RejectRatePct = 300_000+int64((i+8)%2)*5_000, 8
		}
		w.Weeks = append(w.Weeks, f)
	}
	return w
}

// A realized compaction saving counts as prevented, sized by the weekly
// drop over the period after the setting; one not yet shown counts nothing
// and says why.
func TestPreventedCountsARealizedCompactionSaving(t *testing.T) {
	in := withPrevented()
	period := in.Until.Sub(in.Since)
	tests := []struct {
		name   string
		weeks  *Weeks
		count  int
		tokens int64
	}{
		{"no setting", nil, 0, 0},
		{"realized, set before the period", realizedWeeks(in.Since.AddDate(0, 0, -60)), 1, int64(600_000 * period.Hours() / (7 * 24))},
		{"not yet judged", &Weeks{SetAt: in.Since.AddDate(0, 0, -3)}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := withPrevented()
			in.Compaction = tt.weeks
			m, ok := mechanism(Build(in), MechCompaction)
			if !ok {
				t.Fatal("no compaction mechanism")
			}
			if m.Count != tt.count || m.Tokens != tt.tokens || m.Basis == "" {
				t.Errorf("= %+v; want count %d, tokens %d, with a basis", m, tt.count, tt.tokens)
			}
			if _, priced := m.USD.Amount(); priced != (tt.count > 0) {
				t.Errorf("priced = %v for %+v", priced, m)
			}
			if tt.count == 0 && m.USD.Caveat() == "" {
				t.Error("an unrealized saving must say why it counts nothing")
			}
		})
	}
}

// A lockout is avoided when, at the pace recorded before the pause or
// route, the window would have run out before its reset. It is priced in
// the minutes it would have been blocked, counted once per window, and is
// a counterfactual estimate: in prevented only, never in found.
func TestPreventedCountsAvoidedLockouts(t *testing.T) {
	at := t0.Add(day)
	reset := at.Add(2 * time.Hour)
	runsOut := func(d time.Duration) eventschema.WindowForecast {
		return eventschema.WindowForecast{Provider: "anthropic", Window: "5h", UsedPct: 80, PctPerHour: 40,
			RunsOutAt: at.Add(d), ResetsAt: reset}
	}
	tests := []struct {
		name    string
		holds   []Hold
		count   int
		minutes float64
	}{
		{"runs out 30 minutes in, 90 before its reset", []Hold{{At: at, Mechanism: MechBoundary, Forecasts: []eventschema.WindowForecast{runsOut(30 * time.Minute)}}}, 1, 90},
		{"a window that lasts is no lockout", []Hold{{At: at, Mechanism: MechBoundary, Forecasts: []eventschema.WindowForecast{{Window: "5h", UsedPct: 20, ResetsAt: reset}}}}, 0, 0},
		{"already used up is not avoided", []Hold{{At: at, Mechanism: MechBoundary, Forecasts: []eventschema.WindowForecast{runsOut(0)}}}, 0, 0},
		{"two pauses in one window count once, at the first pace", []Hold{
			{At: at, Mechanism: MechBoundary, Forecasts: []eventschema.WindowForecast{runsOut(30 * time.Minute)}},
			{At: at.Add(10 * time.Minute), Mechanism: MechBoundary, Forecasts: []eventschema.WindowForecast{runsOut(20 * time.Minute)}},
		}, 1, 90},
		{"outside the period", []Hold{{At: t0.Add(-day), Mechanism: MechBoundary, Forecasts: []eventschema.WindowForecast{runsOut(30 * time.Minute)}}}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := fixture()
			in.Holds = tt.holds
			r := Build(in)
			m, ok := mechanism(r, MechLockout)
			if !ok {
				t.Fatal("no lockout mechanism")
			}
			if m.Count != tt.count || math.Abs(m.BlockedMinutes-tt.minutes) > 1e-9 {
				t.Errorf("= %d lockouts, %v minutes; want %d, %v", m.Count, m.BlockedMinutes, tt.count, tt.minutes)
			}
			if !m.Counterfactual || !strings.Contains(m.Basis, "counterfactual estimate") {
				t.Errorf("not labelled a counterfactual estimate: %+v", m)
			}
			if _, priced := m.USD.Amount(); priced {
				t.Error("an avoided lockout is priced in minutes, not dollars")
			}
			for _, c := range r.Classes {
				if strings.Contains(c.Name, "lockout") {
					t.Errorf("an avoided lockout reached found: %+v", c)
				}
			}
		})
	}
}

// A boundary decision carries its forecasts into the hold it becomes.
func TestHoldKeepsTheDecisionsForecasts(t *testing.T) {
	f := eventschema.WindowForecast{Window: "5h", UsedPct: 80, ResetsAt: t0}
	env := &eventschema.Envelope{Timestamp: t0, Payload: &eventschema.DecisionEvent{
		Kind: "boundary", Stage: eventschema.DecisionStageApplied, Forecasts: []eventschema.WindowForecast{f}}}
	h, ok := holdOf(env)
	if !ok || len(h.Forecasts) != 1 || h.Forecasts[0] != f {
		t.Errorf("hold = %+v, %v", h, ok)
	}
}

// The lockout mechanism names the windows it counted, so periods added
// together count each window once.
func TestLockoutsNameTheirWindows(t *testing.T) {
	at := t0.Add(day)
	in := fixture()
	in.Holds = []Hold{{At: at, Mechanism: MechBoundary, Forecasts: []eventschema.WindowForecast{{
		Provider: "anthropic", Window: "week", UsedPct: 90, RunsOutAt: at.Add(time.Hour), ResetsAt: at.Add(3 * time.Hour)}}}}
	m, _ := mechanism(Build(in), MechLockout)
	if len(m.Windows) != 1 || m.Windows[0].Window != "week" || m.Windows[0].BlockedMinutes != 120 {
		t.Errorf("windows = %+v", m.Windows)
	}
}

// A lower effort level the developer's weeks judged a saving counts as
// prevented, an estimate scaled to the part of the period after the
// change; one not judged, or judged not realized, counts nothing and says
// why. It is never part of the found figures.
func TestPreventedCountsARealizedEffortSaving(t *testing.T) {
	in := withPrevented()
	period := in.Until.Sub(in.Since)
	realized := func(at time.Time) effortweeks.Watch {
		return effortweeks.Watch{Model: "m", PreventedUSDPerWeek: 70,
			Verdict: effortwatch.Verdict{Judged: true, Realized: true, Change: effortwatch.Change{At: at, From: "xhigh", To: "medium"}}}
	}
	tests := []struct {
		name  string
		watch []effortweeks.Watch
		count int
		usd   float64
	}{
		{"no change observed", nil, 0, 0},
		{"realized before the period", []effortweeks.Watch{realized(in.Since.AddDate(0, 0, -60))}, 1, 70 * period.Hours() / (7 * 24)},
		{"realized mid-period counts from the change", []effortweeks.Watch{realized(in.Since.Add(period / 2))}, 1, 70 * (period / 2).Hours() / (7 * 24)},
		{"not yet judged", []effortweeks.Watch{{Model: "m", Verdict: effortwatch.Verdict{Reason: "waiting"}}}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := withPrevented()
			in.Effort = tt.watch
			r := Build(in)
			m, ok := mechanism(r, MechEffort)
			if !ok {
				t.Fatal("no effort mechanism")
			}
			got, priced := m.USD.Amount()
			if m.Count != tt.count || priced != (tt.count > 0) || math.Abs(got-tt.usd) > 0.01 || m.Basis == "" {
				t.Errorf("= %+v (%v); want count %d, usd %.2f", m, got, tt.count, tt.usd)
			}
			if tt.count > 0 && !strings.Contains(m.Basis, "estimate") {
				t.Errorf("basis %q does not say it is an estimate", m.Basis)
			}
			if tt.count == 0 && m.USD.Caveat() == "" {
				t.Error("an unrealized saving must say why it counts nothing")
			}
		})
	}
}
