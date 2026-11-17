package teamwire

import (
	"math"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

func TestPlanFactor(t *testing.T) {
	for _, tc := range []struct {
		plan string
		want float64
	}{
		{"claude-pro", 1},
		{"claude-max", 5},
		{"claude-max-5x", 5}, // Max's former name
		{"claude-ultra", 10}, // weekly: ten times Pro's week
		{"claude-max-20x", 10},
		{"claude-team-standard", 1.25},
		{"claude-team-premium", 6.25},
		{"claude-enterprise", 0}, // billed at API rates: no allowance
		{"gpt-plus", 1},
		{"gpt-pro-20x", 20},
		{"gpt-pro", 0},    // tier unknown
		{"cursor-pro", 0}, // no stated multiple
		{"pay-as-you-go", 0},
		{"nope", 0},
	} {
		if got := PlanFactor(tc.plan); got != tc.want {
			t.Errorf("PlanFactor(%q) = %v, want %v", tc.plan, got, tc.want)
		}
	}
}

func TestListPriceUSD(t *testing.T) {
	for _, tc := range []struct {
		plan    string
		usd     float64
		perSeat bool
		ok      bool
	}{
		{"claude-ultra", 200, false, true},
		{"claude-max-20x", 200, false, true}, // former name
		{"claude-team-premium", 125, true, true},
		{"copilot-business", 19, true, true},
		{"claude-enterprise", 0, false, false}, // no flat price
		{"gpt-pro", 0, false, false},
		{"subscription", 0, false, false},
		{"nope", 0, false, false},
	} {
		usd, perSeat, ok := ListPriceUSD(tc.plan)
		if usd != tc.usd || perSeat != tc.perSeat || ok != tc.ok {
			t.Errorf("ListPriceUSD(%q) = %v %v %v, want %v %v %v", tc.plan, usd, perSeat, ok, tc.usd, tc.perSeat, tc.ok)
		}
	}
}

func TestSeatAction(t *testing.T) {
	advice := func(plan, verdict, kind, rec string, material bool) PlanAdvice {
		return PlanAdvice{Provider: "anthropic", Plan: plan, Verdict: verdict, Kind: kind, RecommendedPlan: rec,
			Material: material, Confidence: "medium"}
	}
	unmeasured := math.NaN()
	for _, tc := range []struct {
		name        string
		a           PlanAdvice
		limitUsed   float64
		companyPaid bool
		want        string
	}{
		{"stay", advice("claude-max", "stay", "plan", "claude-max", false), 60, true, SeatKeep},
		{"upgrade", advice("claude-max", "upgrade", "plan", "claude-ultra", false), 100, false, SeatUpgrade},
		{"upgrade to overflow", advice("claude-max", "upgrade", "plan_overflow", "claude-ultra", false), 100, false, SeatUpgrade},
		{"move down", advice("claude-ultra", "switch", "plan", "claude-max", true), 30, false, SeatMoveDown},
		{"move down with overflow", advice("claude-ultra", "switch", "plan_overflow", "claude-pro", true), 30, false, SeatMoveDown},
		{"move down from a former name", advice("claude-max-20x", "switch", "plan", "claude-max", true), 30, false, SeatMoveDown},
		{"not material", advice("claude-ultra", "switch", "plan", "claude-max", false), 30, false, SeatKeep},
		{"overflow on the same tier", advice("claude-max", "switch", "plan_overflow", "claude-max", true), 90, false, SeatKeep},
		{"per-seat family", PlanAdvice{Provider: "anthropic", Plan: "claude-team-premium", Verdict: "switch", Kind: "plan",
			RecommendedPlan: "claude-team-standard", Material: true}, 20, true, SeatMoveDown},
		{"cancel", advice("claude-max", "switch", "api", "", true), 4, true, SeatCancel},
		{"cancel needs a company seat", advice("claude-max", "switch", "api", "", true), 4, false, SeatKeep},
		{"used outside the harness", advice("claude-max", "switch", "api", "", true), 45, true, SeatKeep},
		{"at the guard", advice("claude-max", "switch", "api", "", true), 10, true, SeatKeep},
		{"limit not measured", advice("claude-max", "switch", "api", "", true), unmeasured, true, SeatKeep},
		{"negative is not measured", advice("claude-max", "switch", "api", "", true), -1, true, SeatKeep},
		{"API not material", advice("claude-max", "switch", "api", "", false), 2, true, SeatKeep},
		{"unknown verdict", advice("claude-max", "nope", "plan", "claude-pro", true), 2, true, SeatKeep},
	} {
		if got := SeatAction(tc.a, tc.limitUsed, tc.companyPaid); got != tc.want {
			t.Errorf("%s: SeatAction = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLapseMinLeftPctIsPlans(t *testing.T) {
	if LapseMinLeftPct != plans.LapseMinLeftPct {
		t.Fatalf("LapseMinLeftPct = %v, want plans.LapseMinLeftPct %v", LapseMinLeftPct, plans.LapseMinLeftPct)
	}
}

func TestSpendDenominated(t *testing.T) {
	for _, tc := range []struct {
		plan string
		want bool
	}{
		{"pay-as-you-go", true},     // per token, against a limit the operator sets
		{"claude-enterprise", true}, // usage-based Enterprise: API rates from the first token
		{"claude-pro", false},
		{"claude-max-20x", false}, // former name of a windowed plan
		{"claude-team-premium", false},
		{"gpt-pro-20x", false},
		{"cursor-pro", false},
		{"copilot-business", false},
		{"subscription", false},
		{"", false},
		{"nope", false},
	} {
		if got := SpendDenominated(tc.plan); got != tc.want {
			t.Errorf("SpendDenominated(%q) = %v, want %v", tc.plan, got, tc.want)
		}
	}
}

// TestSpendDenominatedFollowsCatalog holds SpendDenominated to the provider
// catalog for every plan and every name a vendor retired, so a plan added
// to the catalog is classified without a change here.
func TestSpendDenominatedFollowsCatalog(t *testing.T) {
	for _, p := range providers.Plans() {
		for _, name := range append([]string{p.Name}, p.FormerNames...) {
			if got := SpendDenominated(name); got != p.SpendDenominated {
				t.Errorf("SpendDenominated(%q) = %v, want the catalog's %v", name, got, p.SpendDenominated)
			}
		}
	}
}
