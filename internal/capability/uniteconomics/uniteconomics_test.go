package uniteconomics

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var t0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func turn(session string, at time.Duration, usd float64) analytics.SessionTurn {
	return analytics.SessionTurn{SessionID: session, At: t0.Add(at), Provider: "anthropic",
		Tokens: int64(usd * 1000), APIEquivalentUSD: usd, Priced: true}
}

func outcome(exec string, at time.Duration, r eventschema.OutcomeResult) *eventschema.Envelope {
	return &eventschema.Envelope{Type: eventschema.EventTypeOutcome, Timestamp: t0.Add(at),
		Association: eventschema.Association{Execution: exec},
		Payload:     &eventschema.OutcomeEvent{Result: r, Assessment: eventschema.OutcomeHuman}}
}

func near(got *float64, want float64) bool { return got != nil && math.Abs(*got-want) < 1e-9 }

func fixture() Inputs {
	linked := func(t analytics.SessionTurn, workflow, exec string) analytics.SessionTurn {
		t.WorkflowID, t.ExecutionID = workflow, exec
		return t
	}
	return Inputs{
		Since: t0, Until: t0.Add(7 * day),
		Dirs:   map[string]sessiondirs.Dir{"a": {CWD: "/w/app"}, "c": {CWD: "/tmp/scratch"}},
		RootOf: map[string]string{"/w/app": "/w/app", "/tmp/scratch": ""},
		CommitsOf: map[string][]gitlog.Commit{"/w/app": {
			{SHA: "0000000000000000", At: t0.Add(-10*day + 30*time.Minute), Subject: "fix: old (#1)"},
			{SHA: "1111111111111111", At: t0.Add(time.Hour), Subject: "feat(x): new"},
			{SHA: "2222222222222222", At: t0.Add(3 * time.Hour), Subject: "fix: y"},
		}},
		PRsOf: map[string][]gitlog.PR{"/w/app": {
			{Number: 1, MergedAt: t0.Add(-10*day + time.Hour)},
			{Number: 2, MergedAt: t0.Add(4 * time.Hour)},
			{Number: 3, MergedAt: t0.Add(6 * day)},
			{Number: 4, MergedAt: t0.Add(6*day + time.Hour)}, // nothing between #3 and it
		}},
		Turns: []analytics.SessionTurn{
			linked(turn("a", -10*day, 10), "wf-old", ""), // baseline
			linked(turn("a", 0, 2), "wf-1", ""),
			turn("a", 2*time.Hour, 4),
			linked(turn("a", 5*day, 8), "", "exec-2"),
			turn("a", 6*day+2*time.Hour, 16), // after the last PR and commit
			turn("c", time.Hour, 32),         // not a repository
			turn("z", time.Hour, 64),         // session directory unknown
		},
		Outcomes: []*eventschema.Envelope{
			outcome("wf-old", -10*day+2*time.Hour, eventschema.OutcomeAchieved),
			outcome("wf-1", 4*time.Hour, eventschema.OutcomeAchieved),
			outcome("exec-2", 5*day+time.Hour, eventschema.OutcomeNotAchieved),
			outcome("exec-3", day, eventschema.OutcomeAchieved), // no work ran under it
			outcome("", day, eventschema.OutcomeAchieved),       // names no execution
		},
		Windows: map[string][]plans.QuotaWindow{"anthropic": {
			// A weekly window that held the baseline turn's 10k tokens at 10%.
			{Label: "weekly", UsedPct: 10, ResetsAt: t0.Add(-5 * day), Duration: 7 * day},
		}},
	}
}

func bucket(u Unit, label string) (Bucket, bool) {
	for _, b := range u.Unplaced {
		if b.Label == label {
			return b, true
		}
	}
	return Bucket{}, false
}

// Cost per commit, per merged pull request and per outcome, each against
// the four weeks before the period, with the work no unit took kept
// visible by why.
func TestBuildUnitCostsAgainstTheBaseline(t *testing.T) {
	r := Build(fixture())
	const total = 2 + 4 + 8 + 16 + 32 + 64

	c := r.Commits
	if c.Current.Units != 2 || !near(c.Current.PerUnitUSD, 3) || c.Baseline.Units != 1 || !near(c.Baseline.PerUnitUSD, 10) || !near(c.ChangePct, -70) {
		t.Errorf("commits = %+v", c)
	}
	if b, _ := bucket(c, "not yet committed"); b.APIEquivalentUSD != 24 {
		t.Errorf("not yet committed = %+v, want $24", b)
	}
	if math.Abs(c.PlacedShare-6.0/total) > 1e-9 {
		t.Errorf("commit placed share = %v", c.PlacedShare)
	}

	p := r.PRs
	if p.Current.Units != 2 || !near(p.Current.PerUnitUSD, 7) || p.Baseline.Units != 1 || !near(p.Baseline.PerUnitUSD, 10) || !near(p.ChangePct, -30) {
		t.Errorf("PRs = %+v", p)
	}
	for label, want := range map[string]float64{"no pull request merged yet": 16, "outside a repository": 32, "session directory unknown": 64} {
		if b, ok := bucket(p, label); !ok || b.APIEquivalentUSD != want {
			t.Errorf("PR bucket %q = %+v, want $%v", label, b, want)
		}
	}
	if b, ok := bucket(p, "merged with no agent work"); !ok || b.Units != 1 {
		t.Errorf("PRs with no agent work = %+v", b)
	}

	o := r.Outcomes
	if o.Current.Units != 3 || !near(o.Current.PerUnitUSD, 10.0/3) || o.Baseline.Units != 1 || !near(o.ChangePct, (10.0/3/10-1)*100) {
		t.Errorf("outcomes = %+v", o)
	}
	a := r.Achieved
	if a.Current.Units != 2 || !near(a.Current.PerUnitUSD, 5) || !near(a.ChangePct, -50) {
		t.Errorf("achieved = %+v", a)
	}
	if b, _ := bucket(o, "no recorded outcome"); b.APIEquivalentUSD != total-10 {
		t.Errorf("no recorded outcome = %+v, want $%v", b, total-10)
	}
	if b, _ := bucket(o, "outcomes with no work joined"); b.Units != 1 {
		t.Errorf("outcomes with no work = %+v", b)
	}

	if len(r.Classes) != 2 || r.Classes[0].Class != "feature" || r.Classes[1].Class != "bugfix" {
		t.Fatalf("classes = %+v", r.Classes)
	}
	if fix := r.Classes[1]; fix.Current.Units != 1 || !near(fix.Current.PerUnitUSD, 4) || !near(fix.ChangePct, -60) {
		t.Errorf("bugfix = %+v", fix)
	}
	if feat := r.Classes[0]; feat.ChangePct != nil || feat.Baseline.Units != 0 {
		t.Errorf("feature has no baseline to compare: %+v", feat)
	}

	// 1,000 tokens per percent of a weekly window: the period's PRs took
	// 14k tokens over two, the baseline's 10k over one.
	if len(r.Calibrations) != 1 || r.Calibrations[0].TokensPerPct != 1000 {
		t.Fatalf("calibrations = %+v", r.Calibrations)
	}
	if s := p.PlanShare; len(s) != 1 || s[0].Window != "weekly" || math.Abs(s[0].Pct-7) > 1e-9 || !near(s[0].BaselinePct, 10) {
		t.Errorf("PR plan share = %+v", s)
	}
	if r.ShareMethod == "" || r.PRs.Method == "" || r.Outcomes.Method == "" {
		t.Error("each figure must say how it was joined")
	}
}

// Nothing recorded is no units and no per-unit figure, never a zero cost.
func TestBuildWithNothingRecorded(t *testing.T) {
	r := Build(Inputs{Since: t0, Until: t0.Add(7 * day)})
	for _, u := range []Unit{r.Commits, r.PRs, r.Outcomes, r.Achieved} {
		if u.Current.Units != 0 || u.Current.PerUnitUSD != nil || u.ChangePct != nil || u.Unplaced == nil {
			t.Errorf("%s = %+v", u.Name, u)
		}
	}
	if len(r.Calibrations) != 0 || r.ShareMethod != "" || r.Classes == nil {
		t.Errorf("report = %+v", r)
	}
}

// The report is derived figures only: no commit subject, session id,
// workflow or execution id, path or repository name leaves in it.
func TestReportCarriesNoFreeText(t *testing.T) {
	b, err := json.Marshal(Build(fixture()))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"feat(x)", "fix: y", "wf-1", "exec-2", "/w/app", "scratch", `"a"`} {
		if strings.Contains(string(b), leak) {
			t.Errorf("report carries %q: %s", leak, b)
		}
	}
}

// OutcomeJoin reads a turn's outcome as the outcome units do: execution,
// then workflow, then session.
func TestOutcomeJoin(t *testing.T) {
	join := OutcomeJoin([]*eventschema.Envelope{
		outcome("exec-1", 0, eventschema.OutcomeAchieved),
		outcome("sess-2", 0, eventschema.OutcomeNotAchieved),
	})
	byExec := turn("sess-9", 0, 1)
	byExec.ExecutionID = "exec-1"
	if r, ok := join(byExec); !ok || r != eventschema.OutcomeAchieved {
		t.Errorf("by execution: %v %v", r, ok)
	}
	if r, ok := join(turn("sess-2", 0, 1)); !ok || r != eventschema.OutcomeNotAchieved {
		t.Errorf("by session: %v %v", r, ok)
	}
	if _, ok := join(turn("sess-3", 0, 1)); ok {
		t.Error("a turn with no outcome was joined")
	}
}
