package boundaries

import (
	"strings"
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

func paceRule(t *testing.T) []domain.Rule {
	return rules(t, domain.RuleSpec{Name: "pace", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"plan_runs_out_early_min": ">= 60"}})
}

// A 5h window 75% used half way through runs out 1h40m before its reset:
// a pace rule holds while a share-left rule at 10% would not.
func TestPaceRuleReadsTheStatusLine(t *testing.T) {
	env := testEnv(t, false)
	write := func(used float64) {
		t.Helper()
		if err := claudelimits.Write(env.LimitsPath, claudelimits.Reading{
			ObservedAt: testNow.Add(-time.Minute),
			FiveHour:   &claudelimits.Window{UsedPct: used, ResetsAt: testNow.Add(150 * time.Minute).Unix()},
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(75)
	res, _ := Guard(paceRule(t), call("s1"), env)
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "Claude 5h runs out 1h 40m before its reset at this pace (>= 60m)") {
		t.Fatalf("ahead of pace = %+v", res)
	}
	write(40)
	if res, _ := Guard(paceRule(t), call("s2"), env); res.Verdict != VerdictNone {
		t.Fatalf("a window that lasts paused: %+v", res)
	}
}

// Without the status line the daemon's figures carry each window's pace,
// and a call that starts another agent reads that agent's too.
func TestPaceRuleReadsTheFigures(t *testing.T) {
	env := testEnv(t, false)
	if err := env.Dir.PutFigures(boundaryledger.Figures{ObservedAt: testNow,
		PlanLeftPct:  map[string]map[string]float64{"anthropic": {"week": 60}, "openai": {"week": 30}},
		PlanEarlyMin: map[string]map[string]float64{"anthropic": {"week": 0}, "openai": {"week": 600}},
	}); err != nil {
		t.Fatal(err)
	}
	if res, _ := Guard(paceRule(t), call("s1"), env); res.Verdict != VerdictNone {
		t.Fatalf("Claude lasts, no agent started: %+v", res)
	}
	cross := call("s2")
	cross.Spends = []string{"openai"}
	res, _ := Guard(paceRule(t), cross, env)
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "Codex week runs out 10h 0m before its reset") {
		t.Fatalf("Codex running out = %+v", res)
	}
}

func TestFiguresCarryThePace(t *testing.T) {
	now := testNow
	week := 7 * 24 * time.Hour
	reports := []plans.HeadroomReport{
		// Half the week gone, 75% used: runs out 56h before the reset.
		{Provider: "anthropic", Windows: []plans.VendorWindow{
			{Name: "week", UsedPct: 75, Duration: week, ResetsAt: now.Add(week / 2)},
			{Name: "5h", UsedPct: 10, Duration: 5 * time.Hour, ResetsAt: now.Add(time.Hour)},
			{Name: "week (Fable)", UsedPct: 99, Duration: week, ResetsAt: now.Add(time.Hour)},
		}},
		// No reset: no pace.
		{Provider: "openai", Windows: []plans.VendorWindow{{Name: "week", UsedPct: 90}}},
	}
	f := Figures(reports, nil, now)
	if got := f.PlanEarlyMin["anthropic"]; got["week"] != 56*60 || got["5h"] != 0 || len(got) != 2 {
		t.Errorf("anthropic pace = %v", got)
	}
	if _, ok := f.PlanEarlyMin["openai"]; ok {
		t.Errorf("a window without a reset has a pace: %v", f.PlanEarlyMin["openai"])
	}
	if !NeedsFigures(paceRule(t)) {
		t.Error("a pace rule needs the daemon's figures")
	}
}
