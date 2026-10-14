// Package uniteconomics answers what a unit of finished work cost: a
// commit, a merged pull request, a recorded outcome, a change of each
// class, and, on a flat-rate plan, how much of a plan window each took.
// Each figure is set against the operator's own baseline: the four weeks
// before the period.
//
// Every join here is a heuristic — work is placed on a commit or a pull
// request by place and time, on an outcome by the execution it names —
// so each unit says how it was joined and keeps the work it could not
// place visible, by why, rather than dropping it.
package uniteconomics

import (
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// BaselineDays is the operator's own baseline: the four weeks before the
// period.
const BaselineDays = 28

// The ways work is joined to each unit, for every surface to show.
const (
	CommitMethod  = commits.Method
	PRMethod      = "time window: each turn goes to the next pull request of yours merged into the default branch of the same repository within 7 days (squash \"(#N)\" and \"Merge pull request #N\" commits, as fetched)"
	OutcomeMethod = "named execution: a turn goes to the outcome recorded for the execution, workflow or session it ran under"
	ClassMethod   = "change class from the Conventional Commits type of each commit's subject (feat, fix, refactor, test, docs, chore); other when none"
	ShareMethod   = "estimated: tokens per percent of the plan's longest window, measured from your own windows that reset in the last five weeks"
)

// PRMaxGap is how long after a turn a pull request may merge and still be
// its result.
const PRMaxGap = 7 * 24 * time.Hour

// Rate is the work placed on some units over one period.
type Rate struct {
	Units int `json:"units"`
	commits.Cost
	// PerUnitUSD is the API-equivalent value per unit: what the work
	// would bill at list prices, its value where a plan covers it. Absent
	// with no units.
	PerUnitUSD *float64 `json:"per_unit_usd,omitempty"`

	tokens map[string]int64
}

func (r *Rate) add(t analytics.SessionTurn) {
	r.Add(t)
	if r.tokens == nil {
		r.tokens = map[string]int64{}
	}
	r.tokens[t.Provider] += t.Tokens
}

func (r *Rate) merge(o Rate) {
	r.Turns += o.Turns
	r.Tokens += o.Tokens
	r.APIEquivalentUSD += o.APIEquivalentUSD
	r.CostUSD += o.CostUSD
	r.UnpricedTurns += o.UnpricedTurns
	for p, n := range o.tokens {
		if r.tokens == nil {
			r.tokens = map[string]int64{}
		}
		r.tokens[p] += n
	}
}

func (r *Rate) finish() {
	if r.Units > 0 {
		v := r.APIEquivalentUSD / float64(r.Units)
		r.PerUnitUSD = &v
	}
}

// Bucket is work, or units, that could not be placed, and why.
type Bucket struct {
	Label string `json:"label"`
	commits.Cost
	// Units counts units no work was placed on (an outcome recorded for
	// an execution no turn ran under).
	Units int `json:"units,omitempty"`
}

// PlanShare is how much of a plan window one unit took, on average.
type PlanShare struct {
	Provider string `json:"provider"`
	Window   string `json:"window"`
	// Pct is the percentage of one window per unit in the period;
	// BaselinePct the same over the baseline.
	Pct         float64  `json:"pct"`
	BaselinePct *float64 `json:"baseline_pct,omitempty"`
}

// Unit is one unit of work's cost, against the baseline.
type Unit struct {
	Name     string `json:"name"`
	Method   string `json:"method"`
	Current  Rate   `json:"current"`
	Baseline Rate   `json:"baseline"`
	// ChangePct is the change in cost per unit against the baseline;
	// absent when either has no units.
	ChangePct *float64 `json:"change_pct,omitempty"`
	// PlacedShare is the share of the period's API-equivalent value
	// placed on a unit.
	PlacedShare float64 `json:"placed_share"`
	// Unplaced is the period's work that went on no unit, by why.
	Unplaced  []Bucket    `json:"unplaced"`
	PlanShare []PlanShare `json:"plan_share,omitempty"`
}

// ClassCost is the cost per commit of one change class.
type ClassCost struct {
	Class     string   `json:"class"`
	Current   Rate     `json:"current"`
	Baseline  Rate     `json:"baseline"`
	ChangePct *float64 `json:"change_pct,omitempty"`
}

// Report is the unit costs over a period against the baseline.
type Report struct {
	Since         time.Time `json:"since"`
	Until         time.Time `json:"until"`
	BaselineSince time.Time `json:"baseline_since"`
	Commits       Unit      `json:"commits"`
	PRs           Unit      `json:"merged_prs"`
	Outcomes      Unit      `json:"outcomes"`
	// Achieved is the cost per achieved outcome: the work on every
	// judged execution, including those that were not achieved, over
	// the achieved ones.
	Achieved    Unit        `json:"achieved_outcomes"`
	Classes     []ClassCost `json:"classes"`
	ClassMethod string      `json:"class_method"`
	// Calibrations are the plan-window sizes the shares are measured
	// with; empty without a flat-rate plan's window readings.
	Calibrations []plans.WindowCalibration `json:"calibrations,omitempty"`
	ShareMethod  string                    `json:"share_method,omitempty"`
}

// Inputs is everything the report is built from: the pure half of
// Compute.
type Inputs struct {
	// Since and Until bound the period; the baseline is the BaselineDays
	// before Since.
	Since, Until time.Time
	// Turns are the turns from the baseline's start to Until.
	Turns     []analytics.SessionTurn
	Dirs      map[string]sessiondirs.Dir
	RootOf    map[string]string
	CommitsOf map[string][]gitlog.Commit
	PRsOf     map[string][]gitlog.PR
	// Outcomes are the outcome events recorded from the baseline's start.
	Outcomes []*eventschema.Envelope
	// Windows are each provider's plan window readings.
	Windows map[string][]plans.QuotaWindow
}

// BaselineSince is where the baseline for a period starting at since
// begins.
func BaselineSince(since time.Time) time.Time { return since.AddDate(0, 0, -BaselineDays) }

// period says which period t falls in: 1 current, 0 baseline, -1 neither.
func (in Inputs) period(t time.Time) int {
	switch {
	case !t.Before(in.Since) && t.Before(in.Until):
		return 1
	case !t.Before(BaselineSince(in.Since)) && t.Before(in.Since):
		return 0
	}
	return -1
}

func (in Inputs) currentTurns() []analytics.SessionTurn {
	var out []analytics.SessionTurn
	for _, t := range in.Turns {
		if in.period(t.At) == 1 {
			out = append(out, t)
		}
	}
	return out
}

func valueOf(ts []analytics.SessionTurn) float64 {
	var v float64
	for _, t := range ts {
		v += t.APIEquivalentUSD
	}
	return v
}

// Build computes the report from in.
func Build(in Inputs) Report {
	r := Report{Since: in.Since, Until: in.Until, BaselineSince: BaselineSince(in.Since),
		ClassMethod: ClassMethod, Classes: []ClassCost{}}
	current := in.currentTurns()
	total := valueOf(current)

	var classes map[taskclass.ChangeClass]*ClassCost
	r.Commits, classes = in.commitUnit(current, total)
	r.PRs = in.prUnit(total)
	r.Outcomes, r.Achieved = in.outcomeUnits(total)
	for _, c := range taskclass.ChangeClasses() {
		if cc := classes[c]; cc != nil {
			cc.Current.finish()
			cc.Baseline.finish()
			cc.ChangePct = change(cc.Current, cc.Baseline)
			r.Classes = append(r.Classes, *cc)
		}
	}

	r.Calibrations = in.calibrations()
	if len(r.Calibrations) > 0 {
		r.ShareMethod = ShareMethod
		for _, u := range []*Unit{&r.PRs, &r.Outcomes, &r.Achieved} {
			u.PlanShare = shares(r.Calibrations, u.Current, u.Baseline)
		}
	}
	return r
}

func change(cur, base Rate) *float64 {
	if cur.PerUnitUSD == nil || base.PerUnitUSD == nil || *base.PerUnitUSD == 0 {
		return nil
	}
	v := (*cur.PerUnitUSD / *base.PerUnitUSD - 1) * 100
	return &v
}

func (u *Unit) finish(placed, total float64) {
	u.Current.finish()
	u.Baseline.finish()
	u.ChangePct = change(u.Current, u.Baseline)
	if total > 0 {
		u.PlacedShare = placed / total
	}
	if u.Unplaced == nil {
		u.Unplaced = []Bucket{}
	}
}

// commitUnit is the cost per commit, and the cost per commit of each
// change class.
func (in Inputs) commitUnit(current []analytics.SessionTurn, total float64) (Unit, map[taskclass.ChangeClass]*ClassCost) {
	u := Unit{Name: "commit", Method: CommitMethod}
	classes := map[taskclass.ChangeClass]*ClassCost{}
	all := commits.Attribute(BaselineSince(in.Since), in.Turns, in.Dirs, in.RootOf, in.CommitsOf)
	for _, c := range all.Commits {
		p := in.period(c.At)
		if p < 0 {
			continue
		}
		class := taskclass.ChangeClassOf(c.Subject)
		cc := classes[class]
		if cc == nil {
			cc = &ClassCost{Class: string(class)}
			classes[class] = cc
		}
		rate, crate := &u.Baseline, &cc.Baseline
		if p == 1 {
			rate, crate = &u.Current, &cc.Current
		}
		for _, r := range []*Rate{rate, crate} {
			r.Units++
			r.merge(Rate{Cost: c.Cost})
		}
	}
	// Unplaced is the period's own turns that went on no commit.
	cur := commits.Attribute(in.Since, current, in.Dirs, in.RootOf, in.CommitsOf)
	u.Unplaced = nonEmpty([]Bucket{
		{Label: "not yet committed", Cost: cur.Uncommitted},
		{Label: "no commit within a day", Cost: cur.NoCommitWithinADay},
		{Label: "outside a repository", Cost: cur.OutsideARepository},
		{Label: "session directory unknown", Cost: cur.UnknownDirectory},
	})
	u.finish(total-sum(u.Unplaced), total)
	return u, classes
}

func sum(bs []Bucket) float64 {
	var v float64
	for _, b := range bs {
		v += b.APIEquivalentUSD
	}
	return v
}

func nonEmpty(bs []Bucket) []Bucket {
	out := []Bucket{}
	for _, b := range bs {
		if b.Turns > 0 || b.Units > 0 {
			out = append(out, b)
		}
	}
	return out
}

// prUnit places each turn on the next pull request of the operator's
// merged in its repository within PRMaxGap.
func (in Inputs) prUnit(total float64) Unit {
	u := Unit{Name: "merged_pr", Method: PRMethod}
	type key struct {
		root string
		n    int
	}
	type placed struct {
		at   time.Time
		rate Rate
	}
	byPR := map[key]*placed{}
	var noPR, tooLate, outside, unknown commits.Cost
	for _, t := range in.Turns {
		inPeriod := in.period(t.At) == 1
		dir, ok := in.Dirs[t.SessionID]
		root := ""
		if ok {
			root = in.RootOf[dir.CWD]
		}
		prs := in.PRsOf[root]
		i := sort.Search(len(prs), func(i int) bool { return !prs[i].MergedAt.Before(t.At) })
		switch {
		case !ok:
			if inPeriod {
				unknown.Add(t)
			}
			continue
		case root == "":
			if inPeriod {
				outside.Add(t)
			}
			continue
		case i == len(prs):
			if inPeriod {
				noPR.Add(t)
			}
			continue
		case prs[i].MergedAt.Sub(t.At) > PRMaxGap:
			if inPeriod {
				tooLate.Add(t)
			}
			continue
		}
		k := key{root, prs[i].Number}
		p := byPR[k]
		if p == nil {
			p = &placed{at: prs[i].MergedAt}
			byPR[k] = p
		}
		p.rate.add(t)
	}
	// A unit is an agent-assisted pull request; one merged in the period
	// with no agent work on it is counted apart.
	withoutWork := 0
	for root, prs := range in.PRsOf {
		for _, pr := range prs {
			if _, ok := byPR[key{root, pr.Number}]; !ok && in.period(pr.MergedAt) == 1 {
				withoutWork++
			}
		}
	}
	for _, p := range byPR {
		switch in.period(p.at) {
		case 1:
			u.Current.Units++
			u.Current.merge(p.rate)
		case 0:
			u.Baseline.Units++
			u.Baseline.merge(p.rate)
		}
	}
	u.Unplaced = nonEmpty([]Bucket{
		{Label: "no pull request merged yet", Cost: noPR},
		{Label: "no pull request within 7 days", Cost: tooLate},
		{Label: "outside a repository", Cost: outside},
		{Label: "session directory unknown", Cost: unknown},
		{Label: "merged with no agent work", Units: withoutWork},
	})
	u.finish(total-sum(u.Unplaced), total)
	return u
}
