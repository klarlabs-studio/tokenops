package config

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// The default headroom pause is a continue rule at 90% of the tightest
// plan window, in shadow: it matches past the threshold and binds nothing.
func TestHeadroomPauseRuleIsAShadowContinueAtNinetyPercent(t *testing.T) {
	spec := HeadroomPauseRule()
	if spec.Name != HeadroomPauseName || spec.Stage != string(boundaries.StageShadow) {
		t.Fatalf("rule = %+v", spec)
	}
	rules, err := boundaries.Parse([]boundaries.RuleSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	r := rules[0]
	if r.Action != boundaries.ActionContinue || r.Require.Kind != boundaries.RequireAsk || r.Enforced() {
		t.Fatalf("parsed = %+v", r)
	}
	if len(r.When) != 1 || r.When[0].Field != boundaries.FieldPlanLeft || r.When[0].Window != boundaries.WindowAny {
		t.Fatalf("condition = %+v, want plan_left_pct on the tightest window", r.When)
	}
	for _, tc := range []struct {
		name  string
		left  map[boundaries.Window]float64
		match bool
	}{
		{"weekly window at 91% used", map[boundaries.Window]float64{boundaries.WindowWeek: 9, boundaries.Window5h: 60}, true},
		{"5h window exactly at 90%", map[boundaries.Window]float64{boundaries.Window5h: 10}, true},
		{"both under 90%", map[boundaries.Window]float64{boundaries.Window5h: 11, boundaries.WindowWeek: 40}, false},
		{"no reading", nil, false},
	} {
		v := boundaries.Evaluate(rules, boundaries.ActionContinue, boundaries.Facts{PlanLeftPct: tc.left})
		if got := len(v.Matches) == 1; got != tc.match {
			t.Errorf("%s: matched = %v, want %v", tc.name, got, tc.match)
		}
		if v.Binding != nil {
			t.Errorf("%s: a shadow rule bound: %+v", tc.name, v.Binding)
		}
	}
}

func TestHasHeadroomPause(t *testing.T) {
	for _, tc := range []struct {
		name  string
		specs []boundaries.RuleSpec
		want  bool
	}{
		{"none", nil, false},
		{"the default", []boundaries.RuleSpec{HeadroomPauseRule()}, true},
		{"the operator's own, any name", []boundaries.RuleSpec{{Name: "plan-floor", Action: "continue",
			When: map[string]string{"plan_left_pct": "< 5", "window": "week"}, Require: "deny"}}, true},
		{"a spend rule is not a headroom pause", []boundaries.RuleSpec{{Name: "session-spend", Action: "continue",
			When: map[string]string{"session_cost_usd": ">= 20"}, Require: "ask"}}, false},
		{"a route rule on headroom is not a pause", []boundaries.RuleSpec{{Name: "route-floor", Action: "model_route",
			When: map[string]string{"plan_left_pct": "< 20"}, Require: "deny"}}, false},
	} {
		if got := (Config{Boundaries: tc.specs}).HasHeadroomPause(); got != tc.want {
			t.Errorf("%s: HasHeadroomPause = %v, want %v", tc.name, got, tc.want)
		}
	}
}
