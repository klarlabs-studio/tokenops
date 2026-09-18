package plans

import "testing"

// Anthropic publishes Max and Team allowances only as multiples of Pro, and
// no longer publishes an absolute for any of them. The catalog held three
// independent absolutes that had drifted out of that relationship: Pro 45
// against Max 5x 50 is 1.1x where the vendor documents 5x.
func TestMaxTiersDeriveFromPro(t *testing.T) {
	pro, ok := Lookup("claude-pro")
	if !ok {
		t.Fatal("claude-pro missing")
	}
	for _, tc := range []struct {
		name string
		mult float64
	}{
		{"claude-max-5x", 5},
		{"claude-max-20x", 20},
		{"claude-team-standard", 1.25},
		{"claude-team-premium", 6.25},
	} {
		got, ok := Lookup(tc.name)
		if !ok {
			t.Errorf("%s missing from the catalog", tc.name)
			continue
		}
		want := int64(float64(pro.MessagesPerWindow)*tc.mult + 0.5)
		if got.MessagesPerWindow != want {
			t.Errorf("%s: MessagesPerWindow = %d, want %d (%.2fx Pro's %d)",
				tc.name, got.MessagesPerWindow, want, tc.mult, pro.MessagesPerWindow)
		}
		if got.RateLimitWindow != pro.RateLimitWindow {
			t.Errorf("%s: window = %v, want Pro's %v", tc.name, got.RateLimitWindow, pro.RateLimitWindow)
		}
	}
}

// One baseline, one place to correct. Deriving is the whole point: the
// vendor stopped publishing absolutes, so a single pinned Pro number is the
// only thing that can go stale, and fixing it fixes every tier.
func TestEveryRelativePlanNamesAPlanThatExists(t *testing.T) {
	for name, p := range catalog {
		if p.RelativeTo == "" {
			continue
		}
		base, ok := catalog[p.RelativeTo]
		if !ok {
			t.Errorf("%s is relative to %q, which is not in the catalog", name, p.RelativeTo)
			continue
		}
		if base.RelativeTo != "" {
			t.Errorf("%s derives from %s, which is itself derived — keep the chain one deep",
				name, p.RelativeTo)
		}
		if p.Multiplier <= 0 {
			t.Errorf("%s is relative to %s with multiplier %v", name, p.RelativeTo, p.Multiplier)
		}
	}
}

// A relative entry must not also carry an absolute; two sources of truth in
// one record is how the Max numbers drifted in the first place.
func TestRelativePlansCarryNoAbsolute(t *testing.T) {
	for name, p := range catalog {
		if p.RelativeTo != "" && p.MessagesPerWindow != 0 {
			t.Errorf("%s has both a multiplier and a hardcoded MessagesPerWindow=%d",
				name, p.MessagesPerWindow)
		}
	}
}

// Team seats are two distinct plans; neither is a guess at "the Team plan".
func TestTeamSeatTiersAreBothPresent(t *testing.T) {
	for _, n := range []string{"claude-team-standard", "claude-team-premium"} {
		if err := Validate(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}

// Enterprise is a plan now, but a spend-denominated one: it has no
// rate-limit window, so binding it without the org spend limit is refused
// rather than defaulted. The earlier design had no entry at all, which left
// an operator on Enterprise reading their tier's absence off a list.
func TestEnterpriseBindsOnlyWithASpendLimit(t *testing.T) {
	if err := Validate("claude-enterprise"); err != nil {
		t.Fatalf("claude-enterprise should be a catalog entry: %v", err)
	}
	err := ValidateSpendLimit("claude-enterprise", 0)
	if err == nil {
		t.Fatal("binding Enterprise without a spend limit should be refused")
	}
	if !containsAny(err.Error(), "spend limit", "--spend-limit") {
		t.Fatalf("the refusal should name what to supply, got: %v", err)
	}
}

// Seat-based Enterprise is an allowance plus metered overflow — two
// denominators at once — and is deliberately still unmodelled. Naming it
// should say so rather than silently resolve to the usage-based entry.
func TestSeatBasedEnterpriseIsNotSilentlyTheUsageBasedPlan(t *testing.T) {
	if _, ok := Lookup("claude-enterprise-seats"); ok {
		t.Fatal("seat-based Enterprise is not modelled and must not resolve")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
