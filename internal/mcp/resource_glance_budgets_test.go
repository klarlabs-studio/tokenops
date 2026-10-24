package mcp

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/budgets"
)

func TestGlanceListsEachBudgetWindowAndItsMode(t *testing.T) {
	got := renderSpendBudgets([]budgets.Reading{
		{Budget: "team", Window: "daily", UsedPct: 82, Mode: "hard", Rule: "budget-team-daily", Enforced: true, Require: "ask"},
		{Budget: "team", Window: "monthly", UsedPct: 40, Mode: "soft"},
		{Budget: "ops", Window: "weekly", UsedPct: 10, Mode: "hard", Rule: "budget-ops-weekly"},
	})
	for _, want := range []string{
		"- team daily: 18% left, 82% spent (hard: `budget-team-daily` asks at the limit)",
		"- team monthly: 60% left, 40% spent (soft: notifies)",
		"- ops weekly: 90% left, 10% spent (hard, in shadow: `budget-ops-weekly` binds nothing yet)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("glance lacks %q:\n%s", want, got)
		}
	}
	if renderSpendBudgets(nil) != "" {
		t.Error("no budgets rendered a section")
	}
}

// A provider budget says whose bill it counts.
func TestGlanceNamesTheProviderABudgetCounts(t *testing.T) {
	got := renderSpendBudgets([]budgets.Reading{
		{Budget: "mine", Window: "monthly", UsedPct: 60, Mode: "soft", Provider: "fireworks"},
	})
	if want := "- mine monthly, billed by Fireworks: 40% left, 60% spent (soft: notifies)"; !strings.Contains(got, want) {
		t.Errorf("glance lacks %q:\n%s", want, got)
	}
}
