package scorecard

import (
	"context"
	"strings"
	"testing"
)

// Unit costs ride on the scorecard as reported figures, beside plan use:
// what a merged pull request cost and how that moved against the four
// weeks before. They are not graded.
func TestScorecardCarriesUnitCosts(t *testing.T) {
	base, change := 10.0, -30.0
	sc := Build(context.Background(), nil, BuildParams{SACPctOverride: 95, UnitCosts: []UnitCost{
		{Unit: "merged PR", Units: 2, PerUnitUSD: 7, BaselinePerUnitUSD: &base, ChangePct: &change},
		{Unit: "achieved outcome", Units: 0},
	}})
	text := sc.String()
	for _, want := range []string{"Unit costs (last 7 days, against the 4 weeks before", "merged PR", "$7.00 each", "2 units", "baseline $10.00", "-30%"} {
		if !strings.Contains(text, want) {
			t.Errorf("scorecard text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "achieved outcome") {
		t.Errorf("a unit with none in the period is not a cost:\n%s", text)
	}
	b, err := sc.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"unit_costs"`) || !strings.Contains(string(b), `"per_unit_usd": 7`) {
		t.Errorf("JSON missing unit costs: %s", b)
	}
	if sc.OverallGrade == "" {
		t.Error("the grade still comes from the KPIs")
	}
}
