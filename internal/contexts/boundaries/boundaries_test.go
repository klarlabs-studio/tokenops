package boundaries

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/policy"
)

func TestParseAcceptsTheADRExample(t *testing.T) {
	rules, err := Parse([]RuleSpec{
		{Name: "session-spend", Action: "continue", When: map[string]string{"session_cost_usd": ">= 20"}, Require: "ask"},
		{Name: "big-subagents", Action: "subagent_model", When: map[string]string{"model_tier": "> sonnet"}, Require: "approver:admin"},
		{Name: "plan-floor", Action: "continue", When: map[string]string{"plan_left_pct": "< 5", "window": "week"}, Require: "deny", Stage: "enforce"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("rules = %d", len(rules))
	}
	if rules[0].Stage != StageShadow {
		t.Errorf("default stage = %q; want shadow", rules[0].Stage)
	}
	if rules[1].Require.Kind != RequireApprover || rules[1].Require.Role != "admin" {
		t.Errorf("require = %+v", rules[1].Require)
	}
	if c := rules[2].When[0]; c.Field != FieldPlanLeft || c.Window != WindowWeek || c.Op != OpLT || c.Number != 5 {
		t.Errorf("plan condition = %+v", c)
	}
	if rules[2].Stage != StageEnforce {
		t.Errorf("stage = %q", rules[2].Stage)
	}
}

func TestParseRefusesWithTheRuleNamed(t *testing.T) {
	cases := []struct {
		name string
		spec RuleSpec
		want string
	}{
		{"unknown action", RuleSpec{Name: "r", Action: "deploy", Require: "ask"}, `"r": action "deploy" is not one of`},
		{"missing name", RuleSpec{Action: "continue", Require: "ask"}, "name is required"},
		{"bad name", RuleSpec{Name: "Has Spaces", Action: "continue", Require: "ask"}, "name"},
		{"bad require", RuleSpec{Name: "r", Action: "continue", Require: "maybe"}, `"r": require "maybe"`},
		{"approver without role", RuleSpec{Name: "r", Action: "continue", Require: "approver:"}, `"r": require "approver:" names no role`},
		{"unknown field", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"prompt": "== x"}}, `"r": when.prompt is not a condition`},
		{"no comparator", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"session_cost_usd": "20"}}, `"r": when.session_cost_usd: "20" needs a comparator`},
		{"not a number", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"session_cost_usd": ">= twenty"}}, `"r": when.session_cost_usd: "twenty" is not a number`},
		{"nan", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"session_cost_usd": ">= NaN"}}, "is not a number"},
		{"negative cost", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"session_cost_usd": ">= -1"}}, "must not be negative"},
		{"pct out of range", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"plan_left_pct": "< 120"}}, "between 0 and 100"},
		{"window alone", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"window": "week"}}, "window qualifies plan_left_pct"},
		{"bad window", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"plan_left_pct": "< 5", "window": "month"}}, `window "month"`},
		{"tier on continue", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"model_tier": "> sonnet"}}, "when.model_tier does not apply to continue rules"},
		{"budget on continue", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"budget_used_pct": ">= 90"}}, "when.budget_used_pct does not apply to continue"},
		{"cost on share", RuleSpec{Name: "r", Action: "share", Require: "ask", When: map[string]string{"session_cost_usd": ">= 1"}}, "does not apply to share rules (they take destination)"},
		{"budget alone", RuleSpec{Name: "r", Action: "spend", Require: "ask", When: map[string]string{"budget": "monthly"}}, "budget qualifies budget_used_pct"},
		{"empty budget", RuleSpec{Name: "r", Action: "spend", Require: "ask", When: map[string]string{"budget_used_pct": ">= 90", "budget": " "}}, "names no budget"},
		{"budget pct out of range", RuleSpec{Name: "r", Action: "spend", Require: "ask", When: map[string]string{"budget_used_pct": ">= 150"}}, "between 0 and 100"},
		{"bad destination", RuleSpec{Name: "r", Action: "share", Require: "ask", When: map[string]string{"destination": "== slack"}}, `destination "slack"`},
		{"destination order", RuleSpec{Name: "r", Action: "share", Require: "ask", When: map[string]string{"destination": "> team"}}, "only takes =="},
		{"bad tier", RuleSpec{Name: "r", Action: "subagent_model", Require: "ask", When: map[string]string{"model_tier": "> gpt"}}, `tier "gpt"`},
		{"id with order", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"agent_id": "> x"}}, "only takes =="},
		{"empty id", RuleSpec{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"workflow_id": "=="}}, "names nothing"},
		{"bad stage", RuleSpec{Name: "r", Action: "continue", Require: "ask", Stage: "live"}, `stage "live"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]RuleSpec{tc.spec})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestParseRefusesDuplicateNames(t *testing.T) {
	_, err := Parse([]RuleSpec{
		{Name: "a", Action: "continue", Require: "ask"},
		{Name: "a", Action: "continue", Require: "deny"},
	})
	if err == nil || !strings.Contains(err.Error(), `"a" is defined twice`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireMapsOntoTheAuthorityLadder(t *testing.T) {
	cases := []struct {
		in   string
		want policy.Authority
	}{
		{"allow", policy.Automatic},
		{"ask", policy.RequireApproval},
		{"approver:admin", policy.RequireApproval},
		{"deny", policy.ObserveOnly},
	}
	for _, tc := range cases {
		r, err := ParseRequire(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Authority(); got != tc.want {
			t.Errorf("%s → %s; want %s", tc.in, got, tc.want)
		}
	}
}

func cost(v float64) Facts { return Facts{SessionCostUSD: Known(v)} }

func TestConditionsCompare(t *testing.T) {
	cases := []struct {
		name  string
		when  map[string]string
		facts Facts
		want  bool
	}{
		{">= at the line", map[string]string{"session_cost_usd": ">= 20"}, cost(20), true},
		{">= below", map[string]string{"session_cost_usd": ">= 20"}, cost(19.99), false},
		{"> at the line", map[string]string{"session_cost_usd": "> 20"}, cost(20), false},
		{"< ", map[string]string{"session_cost_usd": "< 1"}, cost(0.5), true},
		{"<= ", map[string]string{"session_cost_usd": "<= 1"}, cost(1), true},
		{"==", map[string]string{"session_cost_usd": "== 1"}, cost(1), true},
		{"unknown cost never matches", map[string]string{"session_cost_usd": ">= 0"}, Facts{}, false},
		{"plan window named", map[string]string{"plan_left_pct": "< 5", "window": "week"},
			Facts{PlanLeftPct: map[Window]float64{WindowWeek: 3, Window5h: 80}}, true},
		{"plan window other", map[string]string{"plan_left_pct": "< 5", "window": "5h"},
			Facts{PlanLeftPct: map[Window]float64{WindowWeek: 3, Window5h: 80}}, false},
		{"plan any window takes the tightest", map[string]string{"plan_left_pct": "< 5"},
			Facts{PlanLeftPct: map[Window]float64{WindowWeek: 3, Window5h: 80}}, true},
		{"plan window unknown", map[string]string{"plan_left_pct": "< 5", "window": "week"},
			Facts{PlanLeftPct: map[Window]float64{Window5h: 1}}, false},
		{"workflow glob", map[string]string{"workflow_id": "== claude-code:*"}, Facts{WorkflowID: "claude-code:p:s"}, true},
		{"agent exact miss", map[string]string{"agent_id": "== a"}, Facts{AgentID: "b"}, false},
		{"all must hold", map[string]string{"session_cost_usd": ">= 1", "agent_id": "== a"}, Facts{SessionCostUSD: Known(2), AgentID: "b"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := Parse([]RuleSpec{{Name: "r", Action: "continue", Require: "ask", When: tc.when}})
			if err != nil {
				t.Fatal(err)
			}
			_, got := rules[0].Match(ActionContinue, tc.facts)
			if got != tc.want {
				t.Fatalf("match = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestModelTierCompares(t *testing.T) {
	rules, err := Parse([]RuleSpec{{Name: "r", Action: "subagent_model", Require: "ask", When: map[string]string{"model_tier": "> sonnet"}}})
	if err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string]bool{"opus": true, "sonnet": false, "haiku": false, "": false} {
		_, got := rules[0].Match(ActionSubagentModel, Facts{ModelTier: model})
		if got != want {
			t.Errorf("%q: match = %v; want %v", model, got, want)
		}
	}
	if _, got := rules[0].Match(ActionContinue, Facts{ModelTier: "opus"}); got {
		t.Error("a subagent_model rule matched a continue action")
	}
}

func TestStrictestEnforcedRuleWins(t *testing.T) {
	rules, err := Parse([]RuleSpec{
		{Name: "allow-all", Action: "continue", Require: "allow", Stage: "enforce"},
		{Name: "ask-20", Action: "continue", Require: "ask", Stage: "enforce", When: map[string]string{"session_cost_usd": ">= 20"}},
		{Name: "admin-30", Action: "continue", Require: "approver:admin", Stage: "enforce", When: map[string]string{"session_cost_usd": ">= 30"}},
		{Name: "deny-50", Action: "continue", Require: "deny", When: map[string]string{"session_cost_usd": ">= 50"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		cost        float64
		binding     string
		shadowCount int
	}{
		{10, "allow-all", 0},
		{25, "ask-20", 0},
		{35, "admin-30", 0},
		{60, "admin-30", 1}, // deny-50 is in shadow: recorded, not binding
	}
	for _, tc := range cases {
		v := Evaluate(rules, ActionContinue, cost(tc.cost))
		if v.Binding == nil || v.Binding.Rule.Name != tc.binding {
			t.Errorf("cost %v: binding = %+v; want %s", tc.cost, v.Binding, tc.binding)
		}
		if got := len(v.Shadow()); got != tc.shadowCount {
			t.Errorf("cost %v: shadow = %d; want %d", tc.cost, got, tc.shadowCount)
		}
	}
	promoted := Promote(rules, "deny-50")
	if v := Evaluate(promoted, ActionContinue, cost(60)); v.Binding == nil || v.Binding.Rule.Name != "deny-50" {
		t.Fatalf("after promote, binding = %+v", v.Binding)
	}
	if rules[3].Stage != StageShadow {
		t.Fatal("Promote changed its input")
	}
}

func TestNothingMatchesNoVerdict(t *testing.T) {
	v := Evaluate(nil, ActionContinue, cost(1000))
	if v.Binding != nil || len(v.Matches) != 0 || v.Refuses() || v.Asks() {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestCrossedSaysWhatWasCrossed(t *testing.T) {
	rules, _ := Parse([]RuleSpec{{Name: "r", Action: "continue", Require: "deny", When: map[string]string{"session_cost_usd": ">= 20", "plan_left_pct": "< 5", "window": "week"}}})
	m, ok := rules[0].Match(ActionContinue, Facts{SessionCostUSD: Known(23.1), PlanLeftPct: map[Window]float64{WindowWeek: 3.2}})
	if !ok {
		t.Fatal("no match")
	}
	got := m.Crossed()
	for _, want := range []string{"session cost $23.10 >= $20.00", "plan left 3.2% < 5% (week)"} {
		if !strings.Contains(got, want) {
			t.Errorf("crossed = %q; want %q", got, want)
		}
	}
}

func TestCrossedNamesThePlanItRead(t *testing.T) {
	rules, _ := Parse([]RuleSpec{{Name: "r", Action: "continue", Require: "ask", When: map[string]string{"plan_left_pct": "<= 10"}}})
	m, ok := rules[0].Match(ActionContinue, Facts{PlanLeftPct: map[Window]float64{Window5h: 6, WindowWeek: 70}, PlanVendor: "Claude"})
	if !ok {
		t.Fatal("no match")
	}
	if got, want := m.Crossed(), "Claude plan left 6.0% <= 10% (5h)"; !strings.Contains(got, want) {
		t.Errorf("crossed = %q; want %q", got, want)
	}
}

func TestDecisionIDIsStablePerRuleAndSession(t *testing.T) {
	a, b := DecisionID("r", "s1"), DecisionID("r", "s1")
	if a != b || !strings.HasPrefix(a, "bnd-") {
		t.Fatalf("ids %q %q", a, b)
	}
	if DecisionID("r", "s2") == a || DecisionID("q", "s1") == a {
		t.Fatal("ids collide across rule or session")
	}
	if !ValidDecisionID(a) || ValidDecisionID("bnd-../x") {
		t.Fatal("ValidDecisionID")
	}
}

func TestTierOf(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-4-7": "opus", "opus": "opus", "sonnet": "sonnet", "claude-haiku-4-5": "haiku", "deep": "opus",
		"lookup": "haiku", "balanced": "sonnet", "default": "sonnet", "gpt-5": "",
	} {
		if got := TierOf(in); got != want {
			t.Errorf("TierOf(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestPhaseTwoActionsParse(t *testing.T) {
	rules, err := Parse([]RuleSpec{
		{Name: "budget-floor", Action: "spend", When: map[string]string{"budget_used_pct": ">= 90", "budget": "monthly"}, Require: "ask"},
		{Name: "no-opus-routes", Action: "model_route", When: map[string]string{"model_tier": ">= opus"}, Require: "deny"},
		{Name: "team-upload", Action: "share", When: map[string]string{"destination": "== team"}, Require: "ask"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c := rules[0].When[0]; c.Field != FieldBudgetUsed || c.Budget != "monthly" || c.Number != 90 {
		t.Errorf("budget condition = %+v", c)
	}
	if c := rules[2].When[0]; c.Field != FieldDestination || c.Text != DestinationTeam {
		t.Errorf("destination condition = %+v", c)
	}
}

func TestBudgetUsedReadsTheNamedOrTheFullestBudget(t *testing.T) {
	used := map[string]float64{"monthly": 92, "daily": 40}
	named := Condition{Field: FieldBudgetUsed, Op: OpGE, Number: 90, Budget: "daily"}
	if _, ok := named.holds(Facts{BudgetUsedPct: used}); ok {
		t.Error("daily at 40% matched >= 90")
	}
	fullest := Condition{Field: FieldBudgetUsed, Op: OpGE, Number: 90}
	text, ok := fullest.holds(Facts{BudgetUsedPct: used})
	if !ok || !strings.Contains(text, "budget monthly 8.0% left (92.0% spent >= 90%)") {
		t.Errorf("fullest = %q, %v", text, ok)
	}
	if _, ok := fullest.holds(Facts{}); ok {
		t.Error("an unknown budget matched")
	}
	missing := Condition{Field: FieldBudgetUsed, Op: OpGE, Number: 0, Budget: "weekly"}
	if _, ok := missing.holds(Facts{BudgetUsedPct: used}); ok {
		t.Error("a budget that is not measured matched")
	}
}

func TestDestinationMatchesOnlyItsOwn(t *testing.T) {
	c := Condition{Field: FieldDestination, Op: OpEQ, Text: DestinationTeam}
	if text, ok := c.holds(Facts{Destination: DestinationTeam}); !ok || text != "figures leaving for the team plane" {
		t.Errorf("team = %q, %v", text, ok)
	}
	if _, ok := c.holds(Facts{Destination: DestinationOTel}); ok {
		t.Error("otel matched a team rule")
	}
}

func TestEveryActionNamesItsFields(t *testing.T) {
	for _, a := range actions {
		if len(fieldsFor[a]) == 0 {
			t.Errorf("action %s takes no condition", a)
		}
	}
}

func TestCrossingsAreFiguresOnly(t *testing.T) {
	rules, err := Parse([]RuleSpec{{Name: "r", Action: "spend", Require: "ask", Stage: "enforce",
		When: map[string]string{"budget_used_pct": ">= 90", "budget": "secret-project", "session_cost_usd": ">= 5", "agent_id": "== claude-code:*"}}})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := rules[0].Match(ActionSpend, Facts{
		SessionCostUSD: Known(7), BudgetUsedPct: map[string]float64{"secret-project": 95}, AgentID: "claude-code:repo",
	})
	if !ok {
		t.Fatal("no match")
	}
	got := m.Crossings()
	if len(got) != 2 || got[0].Field != FieldSessionCost || got[0].Value != 7 || got[0].Threshold != 5 ||
		got[1].Field != FieldBudgetUsed || got[1].Value != 95 || got[1].Threshold != 90 {
		t.Fatalf("crossings = %+v", got)
	}
}

func TestFromOrgPrefixesAndRoutes(t *testing.T) {
	rules, _ := Parse([]RuleSpec{
		{Name: "big", Action: "subagent_model", Require: "approver:admin", When: map[string]string{"model_tier": "> sonnet"}},
		{Name: "floor", Action: "continue", Require: "ask"},
	})
	org := FromOrg(rules)
	if org[0].Name != "org:big" || org[0].Source != SourceOrg || !org[0].TeamRouted() || org[1].TeamRouted() {
		t.Fatalf("org = %+v", org)
	}
	if rules[0].Name != "big" || rules[0].TeamRouted() {
		t.Fatal("FromOrg changed its input, or a local approver rule is routed")
	}
}
