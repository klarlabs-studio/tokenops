package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func putFigures(t *testing.T, env Env, f boundaryledger.Figures) {
	t.Helper()
	if f.ObservedAt.IsZero() {
		f.ObservedAt = testNow.Add(-time.Minute)
	}
	if err := env.Dir.PutFigures(f); err != nil {
		t.Fatal(err)
	}
}

func TestRouteCheckHoldsUntilApproved(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{
		Name: "no-opus-routes", Action: "model_route", Require: "ask", Stage: "enforce",
		When: map[string]string{"model_tier": ">= opus"},
	})
	check := RouteCheck(func() []Rule { return r }, env)
	if held, _ := check(eventschema.ProviderAnthropic, "claude-sonnet-4-5", "claude-haiku-4-5", "", ""); held {
		t.Fatal("a route down to haiku was held by an opus rule")
	}
	held, reason := check(eventschema.ProviderAnthropic, "claude-sonnet-4-5", "claude-opus-4-1", "", "")
	if !held || !strings.Contains(reason, "model opus >= opus") || !strings.Contains(reason, "stays on the model it asked for") ||
		strings.Contains(reason, "Do not retry") {
		t.Fatalf("held %v, reason %q", held, reason)
	}
	st, _ := RuleStatuses(rules(t, domain.RuleSpec{Name: "no-opus-routes", Action: "model_route", Require: "ask"}), env, nil)
	if len(st.Pending) != 1 {
		t.Fatalf("pending = %+v", st.Pending)
	}
	d, err := Decide(env, DecideRequest{ID: st.Pending[0].ID, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Note, "the route goes through once") {
		t.Errorf("note = %q", d.Note)
	}
	if held, _ := check(eventschema.ProviderAnthropic, "claude-sonnet-4-5", "claude-opus-4-1", "", ""); held {
		t.Fatal("an approved route was held")
	}
	if held, _ := check(eventschema.ProviderAnthropic, "claude-sonnet-4-5", "claude-opus-4-1", "", ""); !held {
		t.Fatal("a one-off approval let a second route through")
	}
}

func TestRouteCheckHoldsNothingWithoutRouteRules(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, spendRule("ask", "enforce"))
	if held, _ := RouteCheck(func() []Rule { return r }, env)(eventschema.ProviderAnthropic, "a", "b", "", ""); held {
		t.Fatal("a route was held with no model_route rule")
	}
	if s, _ := env.Dir.Sessions(); len(s) != 0 {
		t.Fatal("a route with no rule wrote state")
	}
}

func TestCheckShareHoldsOnlyItsDestination(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "team-upload", Action: "share", Require: "deny", Stage: "enforce",
		When: map[string]string{"destination": "== team"}})
	err := CheckShare(r, domain.DestinationTeam, env)
	if !IsShareRefused(err) || !strings.Contains(err.Error(), "figures leaving for the team plane") ||
		!strings.Contains(err.Error(), "The figures are not sent") {
		t.Fatalf("team = %v", err)
	}
	if err := CheckShare(r, domain.DestinationOTel, env); err != nil {
		t.Fatalf("otel = %v", err)
	}
	got := stages(t, env, domain.DecisionID("team-upload", scope("share", "team")))
	if len(got) != 1 || got[0] != eventschema.DecisionStageApplied {
		t.Fatalf("recorded %v", got)
	}
}

func TestCheckShareInShadowSendsAndRecords(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "otel", Action: "share", Require: "deny", When: map[string]string{"destination": "== otel"}})
	if err := CheckShare(r, domain.DestinationOTel, env); err != nil {
		t.Fatalf("a shadow rule held figures: %v", err)
	}
	if got := stages(t, env, domain.DecisionID("otel", scope("share", "otel"))); len(got) != 1 || got[0] != eventschema.DecisionStageShadow {
		t.Fatalf("recorded %v", got)
	}
}

func TestSpendRulesReadTheDaemonsBudgets(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "budget-floor", Action: "spend", Require: "deny", Stage: "enforce",
		When: map[string]string{"budget_used_pct": ">= 90", "budget": "monthly"}})
	c := call("s1")
	if res, _ := GuardActions(r, c, env, domain.ActionContinue, domain.ActionSpend); res.Verdict != VerdictNone {
		t.Fatalf("no figures yet, verdict = %q", res.Verdict)
	}
	putFigures(t, env, boundaryledger.Figures{BudgetUsedPct: map[string]float64{"monthly": 93}})
	res, err := GuardActions(r, c, env, domain.ActionContinue, domain.ActionSpend)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "budget monthly 7.0% left (93.0% spent >= 90%)") ||
		!strings.Contains(res.Reason, "steps while it holds") {
		t.Fatalf("verdict %q, reason %q", res.Verdict, res.Reason)
	}
}

func TestStaleFiguresSayNothing(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "budget-floor", Action: "spend", Require: "deny", Stage: "enforce",
		When: map[string]string{"budget_used_pct": ">= 90"}})
	putFigures(t, env, boundaryledger.Figures{ObservedAt: testNow.Add(-time.Hour), BudgetUsedPct: map[string]float64{"monthly": 99}})
	if res, _ := GuardActions(r, call("s1"), env, domain.ActionSpend); res.Verdict != VerdictNone {
		t.Fatalf("an hour-old reading refused: %q", res.Reason)
	}
}

func TestOpencodeReadsPlanHeadroomFromTheDaemon(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "plan-floor", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"plan_left_pct": "< 5", "window": "week"}})
	putFigures(t, env, boundaryledger.Figures{PlanLeftPct: map[string]map[string]float64{
		"anthropic": {"week": 30}, "openai": {"week": 3, "5h": 80},
	}})
	c := Call{Harness: HarnessOpencode, SessionID: "oc1", Action: domain.ActionContinue}
	res, err := Guard(r, c, env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "plan left 3.0% < 5% (week)") {
		t.Fatalf("verdict %q, reason %q", res.Verdict, res.Reason)
	}
	// Claude Code is held to its own plan, not the tightest one.
	cc := call("cc1")
	if res, _ := Guard(r, cc, env); res.Verdict != VerdictNone {
		t.Fatalf("claude code refused on another provider's plan: %q", res.Reason)
	}
}

func TestFiguresFromTheGlance(t *testing.T) {
	now := testNow
	reports := []plans.HeadroomReport{
		{Provider: "anthropic", Windows: []plans.VendorWindow{
			{Name: "5h", UsedPct: 60, ResetsAt: now.Add(time.Hour)},
			{Name: "week", UsedPct: 96},
			{Name: "week (Fable)", UsedPct: 99},
		}},
		{Provider: "anthropic", Windows: []plans.VendorWindow{{Name: "week", UsedPct: 80}}},
		{Provider: "openai", Windows: []plans.VendorWindow{
			{Name: "5h", UsedPct: 10, ResetsAt: now.Add(-time.Minute)},
			{Name: "week", UsedPct: 50, Stale: true},
		}},
	}
	f := Figures(reports, map[string]float64{"monthly": 42}, now)
	if got := f.PlanLeftPct["anthropic"]; got["5h"] != 40 || got["week"] != 4 || len(got) != 2 {
		t.Errorf("anthropic = %v", got)
	}
	if _, ok := f.PlanLeftPct["openai"]; ok {
		t.Errorf("stale and reset windows were kept: %v", f.PlanLeftPct["openai"])
	}
	if f.BudgetUsedPct["monthly"] != 42 || !f.ObservedAt.Equal(now) {
		t.Errorf("figures = %+v", f)
	}
}

func TestNeedsFigures(t *testing.T) {
	if NeedsFigures(rules(t, spendRule("ask", "enforce"))) {
		t.Error("a session-cost rule needs no daemon figures")
	}
	if !NeedsFigures(rules(t, domain.RuleSpec{Name: "b", Action: "spend", Require: "ask", When: map[string]string{"budget_used_pct": ">= 90"}})) {
		t.Error("a budget rule needs them")
	}
}

func TestStatusShowsTokenOpsSidePoints(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t,
		domain.RuleSpec{Name: "route", Action: "model_route", Require: "ask", Stage: "enforce", When: map[string]string{"model_tier": ">= opus"}},
		domain.RuleSpec{Name: "team", Action: "share", Require: "deny", When: map[string]string{"destination": "== team"}},
		domain.RuleSpec{Name: "all-sharing", Action: "share", Require: "ask"},
	)
	st, err := RuleStatuses(r, env, map[string]bool{rowRouter: true, rowShareOTel: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, rs := range st.Rules {
		for _, h := range rs.Harnesses {
			got[rs.Name+"/"+h.Harness] = h.Enforcement
		}
	}
	want := map[string]string{
		"route/router":            EnforcementEnforced,
		"team/team upload":        EnforcementIdle,
		"all-sharing/team upload": EnforcementIdle,
		"all-sharing/otel export": EnforcementShadow,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q; want %q", k, got[k], w)
		}
	}
	if _, ok := got["team/otel export"]; ok {
		t.Error("a team-only rule was listed for the OTel export")
	}
	if _, ok := got["route/codex"]; ok {
		t.Error("a route rule was listed per harness")
	}
	if st.Note != "" {
		t.Errorf("note = %q; no rule here needs the hook", st.Note)
	}
}

// A hard budget window is enforced by the boundary engine, not a second
// path: it starts in shadow, is promoted by its rule's name, and then
// asks at the pause point once that window, and only that window, is at
// its limit.
func TestHardBudgetWindowAsksAtThePausePoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `budgets:
  - name: team
    windows:
      - window: daily
        limit_usd: 10
        mode: hard
      - window: monthly
        limit_usd: 100
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func() []domain.Rule {
		cfg, err := config.ReadMutable(path)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.BoundaryRules()
	}
	env := testEnv(t, true)
	putFigures(t, env, boundaryledger.Figures{BudgetUsedPct: map[string]float64{"team": 100, "team:daily": 100, "team:monthly": 40}})
	if res, _ := GuardActions(load(), call("s1"), env, domain.ActionSpend); res.Verdict != VerdictNone {
		t.Fatalf("a shadow hard window bound: %q", res.Reason)
	}
	if _, err := Promote(path, "budget-team-daily"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	res, err := GuardActions(load(), call("s2"), env, domain.ActionSpend)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictAsk || !strings.Contains(res.Reason, "budget team:daily used up (100.0% spent >= 100%)") {
		t.Fatalf("verdict %q, reason %q", res.Verdict, res.Reason)
	}
	putFigures(t, env, boundaryledger.Figures{BudgetUsedPct: map[string]float64{"team": 100, "team:daily": 60, "team:monthly": 100}})
	if res, _ := GuardActions(load(), call("s3"), env, domain.ActionSpend); res.Verdict != VerdictNone {
		t.Fatalf("the soft monthly window was enforced: %q", res.Reason)
	}
}

func TestWithBudgetsNamesTheRuleOfAHardWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Budgets = []config.BudgetConfig{{Name: "team", Windows: []config.BudgetWindowConfig{
		{Window: "daily", LimitUSD: 10, Mode: "hard", Stage: "enforce"},
		{Window: "monthly", LimitUSD: 100},
	}}}
	readings := []boundaryledger.BudgetReading{
		{Key: "team:daily", Budget: "team", Window: "daily", Mode: "hard", UsedPct: 50},
		{Key: "team:monthly", Budget: "team", Window: "monthly", Mode: "soft", UsedPct: 20},
	}
	f := WithBudgets(boundaryledger.Figures{}, readings, cfg.BoundaryRules())
	if len(f.Budgets) != 2 || f.Budgets[0].Rule != "budget-team-daily" || !f.Budgets[0].Enforced || f.Budgets[1].Rule != "" {
		t.Errorf("budgets = %+v", f.Budgets)
	}
}
