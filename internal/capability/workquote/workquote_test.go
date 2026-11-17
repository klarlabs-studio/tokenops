package workquote

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	capb "go.klarlabs.de/tokenops/internal/capability/boundaries"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/work/quote"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "q.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func unit(session, harness, role, prompt string, start time.Time) agentdx.Unit {
	return agentdx.Unit{SessionID: session, Harness: harness, Role: role, Prompt: prompt, Model: "claude-opus-4-5",
		Effort: "high", Start: start, End: start.Add(4 * time.Minute), LastActivity: start.Add(5 * time.Minute), Turns: 3}
}

func turn(session, provider string, at time.Time, usd float64, planCovered bool) analytics.SessionTurn {
	return analytics.SessionTurn{SessionID: session, Provider: provider, At: at, APIEquivalentUSD: usd, Priced: true,
		InputTokens: 1000, OutputTokens: 100, PlanCovered: planCovered}
}

// Each of the operator's own instructions is priced from its session's
// turns between its start and its last activity, and keeps the kind of
// work in flight when its words only continue it.
func TestSamplesPriceTheOperatorsOwnInstructions(t *testing.T) {
	since := t0.Add(-quote.Lookback)
	a := t0.Add(-2 * time.Hour)
	units := []agentdx.Unit{
		unit("s1", "claude-code", "", "fix the off-by-one in parser.go", a),
		unit("s1", "claude-code", "", "continue", a.Add(10*time.Minute)),
		unit("s1", "claude-code", agentdx.RoleSubagent, "look up the callers", a.Add(20*time.Minute)),
		unit("s2", "codex", "", "what does this function do?", a),
		unit("s3", "claude-code", "", "fix it", a), // no priced turn
		unit("s1", "claude-code", "", "fix the old bug", since.Add(-time.Hour)),
	}
	turns := []analytics.SessionTurn{
		turn("s1", "anthropic", a.Add(time.Minute), 1, true),
		turn("s1", "anthropic", a.Add(2*time.Minute), 2, true),
		turn("s1", "anthropic", a.Add(11*time.Minute), 4, true),
		turn("s1", "anthropic", a.Add(21*time.Minute), 8, true), // the subagent's
		turn("s2", "openai", a.Add(time.Minute), 0.5, true),
		{SessionID: "s3", At: a.Add(time.Minute), APIEquivalentUSD: 3}, // not priced
	}
	got := Samples(units, turns, since)
	if len(got) != 3 {
		t.Fatalf("samples = %+v, want 3", got)
	}
	want := []struct {
		client, provider, kind string
		usd                    float64
	}{
		{"claude-code", "anthropic", "edit", 3},
		{"codex", "openai", "lookup", 0.5},
		{"claude-code", "anthropic", "edit", 4},
	}
	for i, w := range want {
		s := got[i]
		if s.Client != w.client || s.Provider != w.provider || s.Kind != w.kind || math.Abs(s.USD-w.usd) > 1e-9 {
			t.Errorf("sample %d = %+v, want %+v", i, s, w)
		}
		if s.Tokens == 0 || s.Model != "claude-opus-4-5" || s.Effort != "high" {
			t.Errorf("sample %d = %+v", i, s)
		}
	}
}

// Instructions read through a prompt-facts cache carry the kind, not the
// words.
func TestSamplesReadPromptFacts(t *testing.T) {
	a := t0.Add(-time.Hour)
	u1 := unit("s", "claude-code", "", "", a)
	u1.PromptFacts = agentdx.PromptFacts{Known: true, Kind: "deep"}
	u2 := unit("s", "claude-code", "", "", a.Add(10*time.Minute))
	u2.PromptFacts = agentdx.PromptFacts{Known: true, Continues: true}
	got := Samples([]agentdx.Unit{u1, u2}, []analytics.SessionTurn{
		turn("s", "anthropic", a.Add(time.Minute), 1, true), turn("s", "anthropic", a.Add(11*time.Minute), 1, true),
	}, t0.Add(-quote.Lookback))
	if len(got) != 2 || got[0].Kind != "deep" || got[1].Kind != "deep" {
		t.Fatalf("samples = %+v", got)
	}
}

func TestRatesReadPlanCoveredValuePerProvider(t *testing.T) {
	reset := t0.Add(-24 * time.Hour)
	windows := map[string][]plans.QuotaWindow{
		"anthropic": {{Label: "5-hour", UsedPct: 20, ResetsAt: reset, Duration: 5 * time.Hour}},
	}
	turns := []analytics.SessionTurn{
		turn("s", "anthropic", reset.Add(-time.Hour), 6, true),
		turn("s", "anthropic", reset.Add(-2*time.Hour), 4, true),
		turn("s", "anthropic", reset.Add(-3*time.Hour), 100, false), // billed, not the plan's
		turn("s", "openai", reset.Add(-time.Hour), 100, true),       // another plan
		turn("s", "anthropic", reset.Add(-6*time.Hour), 100, true),  // before the window opened
	}
	got := Rates(windows, turns, t0.Add(-quote.Lookback), t0)
	if len(got) != 1 || got[0].Provider != "anthropic" || math.Abs(got[0].USDPerPct-0.5) > 1e-9 {
		t.Fatalf("rates = %+v, want 10 USD over 20%%", got)
	}
}

type fakeLog struct{ pos sqlite.Position }

func (f *fakeLog) Position(context.Context) (sqlite.Position, error) { return f.pos, nil }

// The basis is computed again only when the event log has moved.
func TestRefreshFollowsTheLog(t *testing.T) {
	dir := t.TempDir()
	computed := 0
	in := Inputs{
		Units: func(context.Context, time.Time, time.Time) ([]agentdx.Unit, error) {
			computed++
			return []agentdx.Unit{unit("s", "claude-code", "", "fix the bug", t0.Add(-time.Hour))}, nil
		},
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) {
			return []analytics.SessionTurn{turn("s", "anthropic", t0.Add(-59*time.Minute), 1, true)}, nil
		},
	}
	log := &fakeLog{pos: sqlite.Position{Rowid: 10}}
	b, did, err := Refresh(context.Background(), log, in, dir, t0)
	if err != nil || !did || len(b.Samples) != 1 || b.Position.Rowid != 10 {
		t.Fatalf("first refresh = %+v, %v, %v", b, did, err)
	}
	if _, did, _ = Refresh(context.Background(), log, in, dir, t0.Add(time.Hour)); did || computed != 1 {
		t.Fatalf("refresh with the log unmoved computed again (%d)", computed)
	}
	log.pos.Rowid = 11
	if _, did, _ = Refresh(context.Background(), log, in, dir, t0.Add(time.Hour)); !did || computed != 2 {
		t.Fatalf("refresh after the log moved did not compute (%d)", computed)
	}
	stored, err := Read(dir)
	if err != nil || stored == nil || stored.Position.Rowid != 11 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, basisFile))
	if strings.Contains(string(raw), "fix the bug") {
		t.Fatal("the basis stores an instruction's words")
	}
}

// basisOf is n synthetic edits on claude-opus-4-5 costing 1..n dollars,
// and a five-hour window at 2 dollars a percent.
func basisOf(n int) *Basis {
	b := &Basis{Version: Version, ComputedAt: t0.Add(-time.Hour)}
	for i := range n {
		b.Samples = append(b.Samples, quote.Sample{Client: "claude-code", Provider: "anthropic", Model: "claude-opus-4-5",
			Effort: "high", Kind: "edit", At: t0.Add(-time.Duration(i+1) * time.Hour), USD: float64(i + 1), Tokens: int64(i+1) * 1000})
	}
	b.Rates = []plans.ValueRate{{Provider: "anthropic", Window: "5-hour", Duration: 5 * time.Hour, Windows: 6, USDPerPct: 2}}
	return b
}

func testEnv(t *testing.T) capb.Env {
	root := t.TempDir()
	return capb.Env{Dir: boundaryledger.Dir(filepath.Join(root, "boundaries")), Now: func() time.Time { return t0 }}
}

func TestPrepareQuotesRecordsAndNamesTheBoundary(t *testing.T) {
	st := openStore(t)
	rules, err := domain.Parse([]domain.RuleSpec{{Name: "five-floor", Action: "continue", Require: "ask", Stage: "enforce",
		When: map[string]string{"plan_left_pct": "< 10", "window": "5h"}}})
	if err != nil {
		t.Fatal(err)
	}
	reports := []plans.HeadroomReport{{Provider: "anthropic", Windows: []plans.VendorWindow{
		{Name: "5h", UsedPct: 85, Duration: 5 * time.Hour, ResetsAt: t0.Add(2 * time.Hour)},
		{Name: "week", UsedPct: 40, Duration: 7 * 24 * time.Hour, ResetsAt: t0.Add(72 * time.Hour)},
	}}}
	d := Deps{Basis: basisOf(20), Reports: reports, Rules: rules, Env: testEnv(t), Store: st}
	r, err := Prepare(context.Background(), d, Request{WorkflowID: "workflow:a", Instruction: "fix the off-by-one in parser.go",
		Model: "claude-opus-4-5"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Quoted() || r.Basis != quote.BasisModelKind || r.P50USD != 10.5 || r.P90USD != 18.1 || r.Provider != "anthropic" {
		t.Fatalf("quote = %+v", r.Quote)
	}
	if len(r.Windows) != 2 || !r.Windows[0].Known || r.Windows[0].P90Pct != 9.1 || r.Windows[1].Known {
		t.Fatalf("windows = %+v", r.Windows)
	}
	if !r.NeedsApproval || r.Refused || len(r.Boundaries) != 1 || r.Boundaries[0].Rule != "five-floor" ||
		!strings.Contains(r.Approval, "never approves its own") || !strings.Contains(r.Summary, "five-floor") {
		t.Fatalf("boundaries = %+v, approval %q, summary %q", r.Boundaries, r.Approval, r.Summary)
	}
	if !r.Recorded || r.Calibration.Cost.Scored != 0 {
		t.Fatalf("recorded = %v, calibration = %+v", r.Recorded, r.Calibration)
	}

	check, err := Review(context.Background(), st, "workflow:a", Actual{CostUSD: 12, CostMeasured: true,
		Window: 5 * time.Hour, SharePct: 12, ShareMeasured: true}, t0.Add(time.Hour))
	if err != nil || check == nil {
		t.Fatalf("review = %+v, %v", check, err)
	}
	if check.Cost == nil || !check.Cost.Within() || check.Share == nil || check.Share.Within() || check.ShareWindow != "5h" {
		t.Fatalf("check = %+v", check)
	}
	if !check.Recorded || check.Calibration.Cost.Scored != 1 || check.Calibration.Share.Scored != 1 ||
		!strings.Contains(check.Summary, "within") || !strings.Contains(check.Summary, "over") {
		t.Fatalf("check = %+v", check)
	}
}

// Too little history gives no number and records nothing to check.
func TestPrepareWithTooLittleHistory(t *testing.T) {
	st := openStore(t)
	r, err := Prepare(context.Background(), Deps{Basis: basisOf(5), Store: st},
		Request{WorkflowID: "workflow:b", Instruction: "fix it", Model: "claude-opus-4-5"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Quoted() || r.P90USD != 0 || r.Recorded || !strings.Contains(r.Summary, "too few") {
		t.Fatalf("result = %+v", r)
	}
	if c, _ := Review(context.Background(), st, "workflow:b", Actual{CostUSD: 1, CostMeasured: true}, t0); c != nil {
		t.Fatalf("an unquoted task was checked: %+v", c)
	}
}

func TestPrepareWithoutABasisSaysSo(t *testing.T) {
	r, err := Prepare(context.Background(), Deps{}, Request{Instruction: "fix it", Model: "m"}, t0)
	if err != nil || r.Status != StatusNoBasis || !strings.Contains(r.Summary, "daemon") {
		t.Fatalf("result = %+v, %v", r, err)
	}
}

// Calibration counts each workflow's latest check once enough exist.
func TestCalibrationAfterEnoughChecks(t *testing.T) {
	st := openStore(t)
	for i := range 12 {
		id := fmt.Sprintf("workflow:%d", i)
		if _, err := Prepare(context.Background(), Deps{Basis: basisOf(20), Store: st},
			Request{WorkflowID: id, Instruction: "fix the bug", Model: "claude-opus-4-5"}, t0); err != nil {
			t.Fatal(err)
		}
		cost := 5.0
		if i < 2 {
			cost = 50
		}
		if _, err := Review(context.Background(), st, id, Actual{CostUSD: cost, CostMeasured: true}, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Calibration(context.Background(), st, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Cost.Enough || c.Cost.Scored != 12 || c.Cost.WithinP90 != 10 || c.Share.Scored != 0 {
		t.Fatalf("calibration = %+v", c)
	}
}
