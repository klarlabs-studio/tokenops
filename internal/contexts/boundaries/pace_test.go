package boundaries

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func TestPlanRunsOutEarly(t *testing.T) {
	parse := func(when map[string]string) Rule {
		t.Helper()
		rs, err := Parse([]RuleSpec{{Name: "pace", Action: "continue", Require: "ask", When: when}})
		if err != nil {
			t.Fatal(err)
		}
		return rs[0]
	}
	any := parse(map[string]string{"plan_runs_out_early_min": ">= 60"})
	week := parse(map[string]string{"plan_runs_out_early_min": ">= 60", "window": "week"})
	if got := any.When[0].String(); got != "plan_runs_out_early_min >= 60" {
		t.Errorf("String = %q", got)
	}
	if got := week.When[0].String(); got != "plan_runs_out_early_min >= 60 (week)" {
		t.Errorf("String = %q", got)
	}

	f := Facts{PlanVendor: "Claude", PlanEarlyMin: map[Window]float64{Window5h: 20, WindowWeek: 130}}
	m, ok := any.Match(ActionContinue, f)
	if !ok || !strings.Contains(m.Crossed(), "Claude week runs out 2h 10m before its reset at this pace (>= 60m)") {
		t.Fatalf("any window = %v %q", ok, m.Crossed())
	}
	if x := m.Crossings(); len(x) != 1 || x[0].Field != FieldPlanEarly || x[0].Value != 130 || x[0].Window != WindowWeek {
		t.Errorf("crossings = %+v", x)
	}
	if _, ok := week.Match(ActionContinue, Facts{PlanEarlyMin: map[Window]float64{Window5h: 200}}); ok {
		t.Error("a 5h run-out held a weekly rule")
	}
	if _, ok := any.Match(ActionContinue, Facts{}); ok {
		t.Error("an unknown pace held")
	}
	f.PlanEarlyVendors = map[Window]string{WindowWeek: "Codex"}
	if m, _ := any.Match(ActionContinue, f); !strings.Contains(m.Crossed(), "Codex week") {
		t.Errorf("per-window vendor = %q", m.Crossed())
	}

	for _, bad := range []map[string]string{
		{"plan_runs_out_early_min": "60"},
		{"plan_runs_out_early_min": ">= -5"},
		{"plan_runs_out_early_min": ">= 20000"},
	} {
		if _, err := Parse([]RuleSpec{{Name: "x", Action: "continue", Require: "ask", When: bad}}); err == nil {
			t.Errorf("%v parsed", bad)
		}
	}
	if _, err := Parse([]RuleSpec{{Name: "x", Action: "spend", Require: "ask",
		When: map[string]string{"plan_runs_out_early_min": ">= 60"}}}); err == nil {
		t.Error("a spend rule took a plan pace")
	}
}

// The reason names the rate the run-out was forecast at, in the words
// glance and the coach use for it.
func TestPlanRunsOutEarlyNamesTheRate(t *testing.T) {
	rs, err := Parse([]RuleSpec{{Name: "pace", Action: "continue", Require: "ask",
		When: map[string]string{"plan_runs_out_early_min": ">= 30"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		basis map[Window]string
		want  string
	}{
		{"average", nil, "Claude 5h runs out 46m before its reset at this pace (>= 30m)"},
		{"recent", map[Window]string{Window5h: plans.BasisRecent}, "Claude 5h runs out 46m before its reset at the last hour's rate (>= 30m)"},
		{"another window's basis", map[Window]string{WindowWeek: plans.BasisRecent}, "Claude 5h runs out 46m before its reset at this pace (>= 30m)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := Facts{PlanVendor: "Claude", PlanEarlyMin: map[Window]float64{Window5h: 46}, PlanEarlyBasis: tc.basis}
			m, ok := rs[0].Match(ActionContinue, f)
			if !ok || m.Crossed() != tc.want {
				t.Errorf("crossed = %v %q, want %q", ok, m.Crossed(), tc.want)
			}
		})
	}
}
