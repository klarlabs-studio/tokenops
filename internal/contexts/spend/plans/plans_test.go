package plans

import (
	"strings"
	"testing"
	"time"
)

func TestCatalogEntriesHaveProviderAndSource(t *testing.T) {
	for _, name := range Names() {
		p, ok := Lookup(name)
		if !ok {
			t.Fatalf("Names() returned %q but Lookup missed it", name)
		}
		if p.Provider == "" {
			t.Errorf("%s: empty Provider", name)
		}
		if p.Display == "" {
			t.Errorf("%s: empty Display", name)
		}
		if !strings.Contains(p.SourceURL, "://") {
			t.Errorf("%s: SourceURL missing scheme: %q", name, p.SourceURL)
		}
		if !strings.Contains(p.SourceURL, "(2026-") {
			t.Errorf("%s: SourceURL missing dated snapshot marker: %q", name, p.SourceURL)
		}
		// Window/cap/unit must be coherent: either all three set or
		// MessagesPerWindow zero (meaning "vendor publishes no number,
		// rate-limit window only").
		if p.MessagesPerWindow > 0 {
			if p.RateLimitWindow <= 0 {
				t.Errorf("%s: MessagesPerWindow=%d but RateLimitWindow is zero", name, p.MessagesPerWindow)
			}
			if p.WindowUnit == "" {
				t.Errorf("%s: MessagesPerWindow set but WindowUnit empty", name)
			}
		}
	}
}

func TestCatalogCoversPublishedPlans(t *testing.T) {
	// Test pins the headline plans the docs reference. Adding a new
	// plan to the catalog is fine; removing one of these requires
	// touching this test and the README in the same PR so users don't
	// silently lose support.
	want := []string{
		"claude-max-5x", "claude-max-20x", "claude-pro",
		"gpt-plus", "gpt-pro", "gpt-pro-5x", "gpt-pro-20x", "gpt-pro-500", "gpt-business",
		"copilot-individual", "copilot-pro-plus", "copilot-max", "copilot-business", "copilot-enterprise",
		"cursor-pro", "cursor-business",
		"cursor-plus", "cursor-premium", "cursor-super", "cursor-ultra", "cursor-teams",
	}
	for _, w := range want {
		if _, ok := Lookup(w); !ok {
			t.Errorf("catalog missing required plan %q", w)
		}
	}
}

func TestProductSurfaceNamesAreNotCanonicalPlans(t *testing.T) {
	for _, name := range []string{"codex-plus", "claude-code-pro", "gpt-team"} {
		if _, ok := catalog[name]; ok {
			t.Errorf("%q must be an alias, not a canonical plan", name)
		}
		if _, aliased := ResolveAlias(name); !aliased {
			t.Errorf("%q should remain accepted as a deprecation alias", name)
		}
	}
	if _, ok := Lookup("claude-code-max"); ok {
		t.Error("ambiguous claude-code-max must require an explicit claude-max or claude-ultra")
	}
}

func TestValidateRejectsUnknownWithSuggestions(t *testing.T) {
	err := Validate("claude-maxx")
	if err == nil {
		t.Fatal("expected error for typo")
	}
	if !strings.Contains(err.Error(), "claude-ultra") {
		t.Errorf("error should list valid plans, got: %v", err)
	}
}

func TestValidateAcceptsKnown(t *testing.T) {
	if err := Validate("claude-max-20x"); err != nil {
		t.Errorf("Validate(claude-max-20x) returned %v", err)
	}
}

// Anthropic renamed Max 5x to Max and Max 20x to Ultra on 2026-10-30. The
// retired names mean the plans they became; "claude-max" is the $100 plan
// again, no longer the pre-v0.6 alias of Max 20x.
func TestRetiredAnthropicNamesResolve(t *testing.T) {
	for old, want := range map[string]string{"claude-max-5x": "claude-max", "claude-max-20x": "claude-ultra"} {
		if got, aliased := ResolveAlias(old); !aliased || got != want {
			t.Errorf("ResolveAlias(%q) = %q %v, want %q", old, got, aliased, want)
		}
		if p, ok := Lookup(old); !ok || p.Name != want {
			t.Errorf("Lookup(%q) = %q %v, want %q", old, p.Name, ok, want)
		}
		if err := Validate(old); err != nil {
			t.Errorf("Validate(%q): %v", old, err)
		}
	}
	if got, aliased := ResolveAlias("claude-max"); aliased {
		t.Errorf("claude-max is a catalog plan, not an alias of %q", got)
	}
	if p, ok := Lookup("claude-max"); !ok || p.Display != "Claude Max" || p.MonthlyUSD != 100 {
		t.Errorf("Lookup(claude-max) = %+v %v", p, ok)
	}
}

// Ultra has no 5-hour limit and ten times Pro's week: its shortest window
// is the week, and it claims no message count of its own.
func TestUltraHasNoSessionWindow(t *testing.T) {
	p, ok := Lookup("claude-ultra")
	if !ok || !p.NoSessionLimit || p.MessagesPerWindow != 0 || p.RateLimitWindow != 7*24*time.Hour || p.WeeklyMultiplier != 10 {
		t.Fatalf("claude-ultra = %+v %v", p, ok)
	}
	if m, _ := Lookup("claude-max"); m.MessagesPerWindow != 5*catalog["claude-pro"].MessagesPerWindow {
		t.Errorf("claude-max allows %d per window, want five times Pro's", m.MessagesPerWindow)
	}
}

func TestEveryAliasResolvesToCatalogEntry(t *testing.T) {
	for alias, modern := range deprecatedAliases {
		if _, ok := catalog[modern]; !ok {
			t.Errorf("alias %q maps to missing catalog entry %q", alias, modern)
		}
	}
}

func TestForVendorPlanType(t *testing.T) {
	for in, want := range map[string]string{"plus": "gpt-plus", "prolite": "gpt-pro-5x", " ProLite ": "gpt-pro-5x", "pro": "gpt-pro-20x"} {
		if got, ok := ForVendorPlanType("openai", in); !ok || got != want {
			t.Errorf("ForVendorPlanType(openai, %q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if got, ok := ForVendorPlanType("anthropic", "prolite"); ok {
		t.Errorf("a plan type matched across providers: %q", got)
	}
}

func TestProTiersArePricedInDollars(t *testing.T) {
	for name, want := range map[string]float64{"gpt-pro-5x": 100, "gpt-pro-20x": 200, "gpt-pro-500": 500} {
		if p, _ := Lookup(name); p.MonthlyUSD != want {
			t.Errorf("%s = $%v, want $%v", name, p.MonthlyUSD, want)
		}
	}
}

func TestCovers(t *testing.T) {
	for name, want := range map[string]bool{
		"claude-max-20x":    true,
		"gpt-pro-5x":        true,
		"claude-enterprise": false, // billed at API rates
		"":                  false,
		"not-in-catalog":    true, // still meant as a subscription
	} {
		if got := Covers(name); got != want {
			t.Errorf("Covers(%q) = %v, want %v", name, got, want)
		}
	}
}

// Any provider billed per token can be measured against its own limit.
func TestPayAsYouGo(t *testing.T) {
	if err := Validate(PayAsYouGo); err != nil {
		t.Fatal(err)
	}
	p, ok := Lookup(PayAsYouGo)
	if !ok || !p.SpendDenominated || Covers(PayAsYouGo) {
		t.Errorf("pay-as-you-go = %+v; want billed, measured against a spend limit", p)
	}
	if err := ValidateSpendLimit(PayAsYouGo, 0, false); err == nil {
		t.Error("a pay-as-you-go binding with no limit has no denominator")
	}
	r, err := ComputeHeadroom(PayAsYouGo, HeadroomInputs{SpendUSD: 50, SpendLimitUSD: 200})
	if err != nil || r.SpendPct != 25 {
		t.Errorf("headroom %+v, %v; want 25%%", r, err)
	}
}
