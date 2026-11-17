package plans

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestValueTier(t *testing.T) {
	for _, tc := range []struct {
		plan  string
		ok    bool
		price float64
	}{
		{"cursor-pro", true, 20},
		{"cursor-plus", true, 8},
		{"cursor-teams", true, 0}, // a seat's price depends on its tier: none in the catalog
		{"copilot-individual", true, 10},
		{"copilot-business", true, 19},
		{"gpt-pro", true, 0},            // tier unspecified: no multiple and no price
		{"claude-max", false, 0},        // tiers with stated multiples are replayed
		{"claude-pro", false, 0},        // the base of a family is replayed too
		{"claude-enterprise", false, 0}, // billed per token: no fee to weigh
		{PayAsYouGo, false, 0},          // billed per token
		{Subscription, false, 0},        // an unnamed tier: no product to value
		{"no-such-plan", false, 0},      // unknown to the catalog
		{"", false, 0},
	} {
		tier, ok := ValueTier(tc.plan)
		if ok != tc.ok {
			t.Errorf("ValueTier(%q) ok = %v, want %v", tc.plan, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		p, _ := Lookup(tc.plan)
		if tier.Name != p.Name || tier.Display != p.Display || tier.MonthlyUSD != p.MonthlyUSD {
			t.Errorf("ValueTier(%q) = %+v, want the catalog's %s at $%v", tc.plan, tier, p.Name, p.MonthlyUSD)
		}
		if p.MonthlyUSD > 0 && tier.PriceSource != "list" {
			t.Errorf("ValueTier(%q) price source = %q, want list", tc.plan, tier.PriceSource)
		}
		if tc.price != 0 && tier.MonthlyUSD != tc.price {
			t.Errorf("ValueTier(%q) price = %v, want %v", tc.plan, tier.MonthlyUSD, tc.price)
		}
	}
}

// A plan whose tiers cannot be replayed gets its value at API list price
// and its value multiple, labelled an estimate, and a recommendation to
// keep: no tier move and no cancellation, whatever the multiple.
func TestValueOnly(t *testing.T) {
	until := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-30 * 24 * time.Hour)
	cursor, _ := ValueTier("cursor-pro")
	for _, tc := range []struct {
		name     string
		value    float64
		multiple float64
	}{
		// $60 over 30 days against $20 x 30/30.44.
		{"carries more than it costs", 60, 3.04},
		// Barely used: still keep, since no limit was measured.
		{"barely used", 1, 0.05},
		{"idle", 0, 0},
	} {
		a := ValueOnly(ReplayInput{Provider: "cursor", Since: since, Until: until, Current: cursor,
			ListValue: flatValue(tc.value)})
		if !a.ValueOnly || a.Plan != "cursor-pro" || a.Days != 30 || a.WindowsMeasured != 0 || a.Confidence != ConfidenceLow {
			t.Fatalf("%s: advice = %+v", tc.name, a)
		}
		if a.ListValueUSD == nil || *a.ListValueUSD != tc.value {
			t.Errorf("%s: list value = %v, want %v", tc.name, a.ListValueUSD, tc.value)
		}
		if a.ValueMultiple == nil || math.Abs(*a.ValueMultiple-tc.multiple) > 0.01 {
			t.Errorf("%s: value multiple = %v, want %.2f", tc.name, a.ValueMultiple, tc.multiple)
		}
		r := a.Recommendation
		if r == nil || r.Verdict != VerdictStay || r.Kind != OptionPlan || r.Plan != "cursor-pro" || r.SavingsUSD != 0 || r.Material {
			t.Errorf("%s: recommendation = %+v, want stay on the plan in force", tc.name, r)
			continue
		}
		if !strings.Contains(r.Summary, "estimate") || !strings.Contains(r.Summary, "Cursor Pro") {
			t.Errorf("%s: summary = %q", tc.name, r.Summary)
		}
		if len(a.Options) != 1 || !a.Options[0].Current || a.Options[0].Kind != OptionPlan || a.Options[0].MonthlyUSD == nil ||
			*a.Options[0].MonthlyUSD != 20 {
			t.Errorf("%s: options = %+v, want the plan in force alone", tc.name, a.Options)
		}
		caveats := strings.Join(a.Caveats, " ")
		if !strings.Contains(caveats, "underlying models' API list price") || !strings.Contains(caveats, "no other tier") {
			t.Errorf("%s: caveats = %q", tc.name, caveats)
		}
		if a.WorthSaying() {
			t.Errorf("%s: value-only advice is not for an ambient surface", tc.name)
		}
	}
}

func TestValueOnlyEdges(t *testing.T) {
	until := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	since := until.Add(-30 * 24 * time.Hour)
	cursor, _ := ValueTier("cursor-pro")

	// No rate card: nothing to value, so nothing is recommended.
	a := ValueOnly(ReplayInput{Provider: "cursor", Since: since, Until: until, Current: cursor})
	if a.Recommendation != nil || a.ListValueUSD != nil || !a.ValueOnly {
		t.Errorf("unpriced: %+v", a)
	}

	// A seat with no price: the value without a multiple, still keep.
	teams, _ := ValueTier("cursor-teams")
	a = ValueOnly(ReplayInput{Provider: "cursor", Since: since, Until: until, Current: teams, ListValue: flatValue(50)})
	if a.ListValueUSD == nil || *a.ListValueUSD != 50 || a.ValueMultiple != nil {
		t.Errorf("unpriced seat: value %v, multiple %v", a.ListValueUSD, a.ValueMultiple)
	}
	if r := a.Recommendation; r == nil || r.Verdict != VerdictStay || !strings.Contains(r.Summary, "no price") {
		t.Errorf("unpriced seat: recommendation = %+v", r)
	}

	// Ten days of history in a 30-day period: the ten days are valued.
	a = ValueOnly(ReplayInput{Provider: "cursor", Since: since, Until: until, HistoryFrom: until.Add(-10 * 24 * time.Hour),
		Current: cursor, ListValue: flatValue(20)})
	if a.Days != 10 {
		t.Errorf("days = %v, want the 10 the history covers", a.Days)
	}

	// Requests with no rate are named.
	a = ValueOnly(ReplayInput{Provider: "cursor", Since: since, Until: until, Current: cursor,
		ListValue: func(time.Time, time.Time) (float64, int) { return 10, 3 }})
	if !strings.Contains(strings.Join(a.Caveats, " "), "3 request(s)") {
		t.Errorf("caveats = %q", a.Caveats)
	}

	// An empty period values nothing.
	a = ValueOnly(ReplayInput{Provider: "cursor", Since: until, Until: until, Current: cursor, ListValue: flatValue(10)})
	if a.Recommendation != nil || a.ListValueUSD != nil {
		t.Errorf("empty period: %+v", a)
	}
}
