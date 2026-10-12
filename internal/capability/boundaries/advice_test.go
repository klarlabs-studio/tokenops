package boundaries

import (
	"strings"
	"testing"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
)

func TestAdviceTellsWhatHoldsWithoutASession(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t,
		domain.RuleSpec{Name: "plan-floor", Action: "continue", Require: "deny", Stage: "enforce",
			When: map[string]string{"plan_left_pct": "< 5", "window": "week"}},
		domain.RuleSpec{Name: "budget", Action: "spend", Require: "approver:lead", Stage: "enforce",
			When: map[string]string{"budget_used_pct": ">= 90"}},
		domain.RuleSpec{Name: "shadowed", Action: "continue", Require: "deny", When: map[string]string{"plan_left_pct": "< 50"}},
		spendRule("ask", "enforce"), // needs the session's cost: not judged
	)
	reports := []plans.HeadroomReport{{Provider: "openai", Windows: []plans.VendorWindow{{Name: "week", UsedPct: 97}}}}
	got := AdviceNow(r, env, reports)
	if len(got) != 1 || got[0].Rule != "plan-floor" || !strings.Contains(got[0].Advice, "stop and tell them") {
		t.Fatalf("advice from the glance = %+v", got)
	}
	putFigures(t, env, boundaryledger.Figures{BudgetUsedPct: map[string]float64{"monthly": 95}})
	got = AdviceNow(r, env, reports)
	if len(got) != 2 || got[1].Rule != "budget" || !strings.Contains(got[1].Advice, "approval from lead") {
		t.Fatalf("advice from the daemon's figures = %+v", got)
	}
	if AdviceNow(nil, env, reports) != nil {
		t.Fatal("advice with no rules")
	}
}
