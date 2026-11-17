package boundaries

import (
	"strings"
	"testing"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// A quote asks which rule a p90 task would trip: the plan as it would
// stand after the task, and the task's own cost as a session's.
func TestQuoteTripsProjectTheP90(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t,
		domain.RuleSpec{Name: "week-floor", Action: "continue", Require: "ask", Stage: "enforce",
			When: map[string]string{"plan_left_pct": "< 10", "window": "week"}},
		domain.RuleSpec{Name: "five-floor", Action: "continue", Require: "deny", Stage: "enforce",
			When: map[string]string{"plan_left_pct": "< 5", "window": "5h"}},
		domain.RuleSpec{Name: "dear-session", Action: "spend", Require: "approver:lead", Stage: "enforce",
			When: map[string]string{"session_cost_usd": ">= 20"}},
		domain.RuleSpec{Name: "watching", Action: "continue", Require: "deny",
			When: map[string]string{"plan_left_pct": "< 15", "window": "week"}},
		domain.RuleSpec{Name: "per-workflow", Action: "continue", Require: "deny", Stage: "enforce",
			When: map[string]string{"workflow_id": "== w1"}},
	)
	reports := []plans.HeadroomReport{
		{Provider: "anthropic", Windows: []plans.VendorWindow{{Name: "week", UsedPct: 84}, {Name: "5h", UsedPct: 50}}},
		{Provider: "openai", Windows: []plans.VendorWindow{{Name: "week", UsedPct: 99}}},
	}
	tests := []struct {
		name    string
		shares  map[domain.Window]float64
		usd     float64
		want    []string
		already []string
	}{
		{name: "fits everything", shares: map[domain.Window]float64{"week": 0.5, "5h": 10}, usd: 5},
		{name: "the week drops under its floor", shares: map[domain.Window]float64{"week": 7, "5h": 10}, usd: 5,
			want: []string{"week-floor", "watching"}},
		{name: "a dear task trips the session cost", shares: nil, usd: 25, want: []string{"dear-session"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := QuoteTrips(r, env, reports, "anthropic", tc.shares, tc.usd)
			var names []string
			for _, g := range got {
				names = append(names, g.Rule)
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("trips = %+v, want %v", got, tc.want)
			}
			for _, g := range got {
				if g.Already {
					t.Errorf("%s holds already, before the task", g.Rule)
				}
				if g.Rule == "watching" && (g.Enforced || g.NeedsApproval) {
					t.Errorf("a shadow rule needs approval: %+v", g)
				}
				if g.Rule == "week-floor" && (!g.NeedsApproval || !strings.Contains(g.Advice, "operator")) {
					t.Errorf("week-floor = %+v", g)
				}
				if g.Rule == "dear-session" && (!g.NeedsApproval || !strings.Contains(g.Crossed, "session")) {
					t.Errorf("dear-session = %+v", g)
				}
			}
		})
	}
}

// A rule that already holds before the task is reported as such.
func TestQuoteTripsSayWhatHoldsAlready(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "floor", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"plan_left_pct": "< 20", "window": "week"}})
	reports := []plans.HeadroomReport{{Provider: "anthropic", Windows: []plans.VendorWindow{{Name: "week", UsedPct: 90}}}}
	got := QuoteTrips(r, env, reports, "anthropic", map[domain.Window]float64{"week": 1}, 1)
	if len(got) != 1 || !got[0].Already || !got[0].Refuses {
		t.Fatalf("trips = %+v", got)
	}
}
