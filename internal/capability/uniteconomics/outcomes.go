package uniteconomics

import (
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// judged is one execution an outcome was recorded for.
type judged struct {
	at     time.Time
	result eventschema.OutcomeResult
	rate   Rate
}

// judgedExecutions resolves the outcomes recorded for each execution to
// the strongest assessment, dated by the last one. An execution whose
// assessments leave it unknown is not a unit.
func judgedExecutions(envs []*eventschema.Envelope) map[string]*judged {
	byExec := map[string][]*eventschema.Envelope{}
	for _, e := range envs {
		if e == nil || e.Association.Execution == "" {
			continue
		}
		if _, ok := e.Payload.(*eventschema.OutcomeEvent); ok {
			byExec[e.Association.Execution] = append(byExec[e.Association.Execution], e)
		}
	}
	out := map[string]*judged{}
	for id, es := range byExec {
		res := outcomes.Resolve(es)
		if res.Result == eventschema.OutcomeUnknown || res.Result == "" {
			continue
		}
		j := &judged{result: res.Result}
		for _, e := range es {
			if e.Timestamp.After(j.at) {
				j.at = e.Timestamp
			}
		}
		out[id] = j
	}
	return out
}

// judgedFor is the judged execution a turn ran under: its execution's,
// else its workflow's, else its session's; nil when none was judged.
func judgedFor(execs map[string]*judged, t analytics.SessionTurn) *judged {
	for _, id := range []string{t.ExecutionID, t.WorkflowID, t.SessionID} {
		if j := execs[id]; id != "" && j != nil {
			return j
		}
	}
	return nil
}

// OutcomeJoin is the join the outcome units use, for another report to
// read the same way: for a turn, the result recorded for the execution,
// workflow or session it ran under, and false when none was.
func OutcomeJoin(outcomes []*eventschema.Envelope) func(analytics.SessionTurn) (eventschema.OutcomeResult, bool) {
	execs := judgedExecutions(outcomes)
	return func(t analytics.SessionTurn) (eventschema.OutcomeResult, bool) {
		if j := judgedFor(execs, t); j != nil {
			return j.result, true
		}
		return "", false
	}
}

// outcomeUnits are the cost per recorded outcome and per achieved
// outcome. A turn goes to the outcome recorded for the execution it ran
// under, else its workflow's, else its session's.
func (in Inputs) outcomeUnits(total float64) (Unit, Unit) {
	all := Unit{Name: "outcome", Method: OutcomeMethod}
	achieved := Unit{Name: "achieved_outcome", Method: OutcomeMethod}
	execs := judgedExecutions(in.Outcomes)
	var none commits.Cost
	for _, t := range in.Turns {
		j := judgedFor(execs, t)
		if j == nil {
			if in.period(t.At) == 1 {
				none.Add(t)
			}
			continue
		}
		j.rate.add(t)
	}
	withoutWork := 0
	for _, j := range execs {
		p := in.period(j.at)
		if p < 0 {
			continue
		}
		if p == 1 && j.rate.Turns == 0 {
			withoutWork++
		}
		a, b := &all.Baseline, &achieved.Baseline
		if p == 1 {
			a, b = &all.Current, &achieved.Current
		}
		a.Units++
		a.merge(j.rate)
		// The achieved unit carries the work on every judged execution:
		// what failed was spent getting to what succeeded.
		b.merge(j.rate)
		if j.result == eventschema.OutcomeAchieved {
			b.Units++
		}
	}
	for _, u := range []*Unit{&all, &achieved} {
		u.Unplaced = nonEmpty([]Bucket{
			{Label: "no recorded outcome", Cost: none},
			{Label: "outcomes with no work joined", Units: withoutWork},
		})
		u.finish(total-sum(u.Unplaced), total)
	}
	return all, achieved
}

// calibrations measure each provider's plan window over the baseline and
// the period, from the turns recorded while each window was open.
func (in Inputs) calibrations() []plans.WindowCalibration {
	byProvider := map[string][]analytics.SessionTurn{}
	for _, t := range in.Turns {
		byProvider[t.Provider] = append(byProvider[t.Provider], t)
	}
	providers := make([]string, 0, len(in.Windows))
	for p := range in.Windows {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	var out []plans.WindowCalibration
	for _, p := range providers {
		turns := byProvider[p]
		tokensIn := func(from, to time.Time) int64 {
			var n int64
			for _, t := range turns {
				if !t.At.Before(from) && t.At.Before(to) {
					n += t.Tokens
				}
			}
			return n
		}
		if c, ok := plans.Calibrate(p, in.Windows[p], BaselineSince(in.Since), in.Until, tokensIn); ok {
			out = append(out, c)
		}
	}
	return out
}

// shares states the tokens per unit as a share of each calibrated window.
func shares(cs []plans.WindowCalibration, cur, base Rate) []PlanShare {
	var out []PlanShare
	for _, c := range cs {
		if cur.Units == 0 || cur.tokens[c.Provider] == 0 {
			continue
		}
		s := PlanShare{Provider: c.Provider, Window: c.Window, Pct: c.Share(cur.tokens[c.Provider]) / float64(cur.Units)}
		if base.Units > 0 && base.tokens[c.Provider] > 0 {
			v := c.Share(base.tokens[c.Provider]) / float64(base.Units)
			s.BaselinePct = &v
		}
		out = append(out, s)
	}
	return out
}
