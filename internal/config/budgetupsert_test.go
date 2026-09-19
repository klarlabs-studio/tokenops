package config

import (
	"errors"
	"strings"
	"testing"
)

// The operator's real config carries a token ceiling. An agent that only
// wanted to move the warning threshold used to rebuild the budget from its
// own inputs, and a budget rebuilt without limit_tokens and basis is a
// budget that no longer watches anything — or, worse, one that validates
// against a USD limit it was never given.
func TestUpsertBudgetMergesOntoExisting(t *testing.T) {
	c := Default()
	c.Budgets = []BudgetConfig{{
		Name: "weekly-tokens", Window: "weekly",
		LimitTokens: 15_000_000_000, Basis: "tokens",
	}}

	got, err := c.UpsertBudget(BudgetUpdate{Name: "weekly-tokens", WarnAt: 0.6})
	if err != nil {
		t.Fatalf("UpsertBudget: %v", err)
	}
	if got.Created {
		t.Error("an edit reported as a new budget")
	}
	want := BudgetConfig{
		Name: "weekly-tokens", Window: "weekly",
		LimitTokens: 15_000_000_000, Basis: "tokens", WarnAt: 0.6,
	}
	if len(c.Budgets) != 1 || c.Budgets[0] != want {
		t.Errorf("budgets = %+v; want [%+v]", c.Budgets, want)
	}
	if got.Budget != want {
		t.Errorf("returned budget = %+v; want %+v", got.Budget, want)
	}
}

// A ceiling is what makes a budget a budget. The terminal refused one
// without it; the agent surface did not, and wrote a limit nothing could
// ever reach.
func TestUpsertBudgetRequiresACeiling(t *testing.T) {
	c := Default()
	_, err := c.UpsertBudget(BudgetUpdate{Name: "nothing", Window: "weekly", WarnAt: 0.5})
	if !errors.Is(err, ErrBudgetNoCeiling) {
		t.Fatalf("err = %v; want ErrBudgetNoCeiling", err)
	}
	if len(c.Budgets) != 0 {
		t.Errorf("a budget without a ceiling was added: %+v", c.Budgets)
	}
}

// Flat-rate plans bill $0 at the margin, so a token ceiling is the only
// one that can trip for them. The upsert must accept it.
func TestUpsertBudgetAcceptsTokenBasis(t *testing.T) {
	c := Default()
	got, err := c.UpsertBudget(BudgetUpdate{
		Name: "weekly-tokens", Window: "WEEKLY", LimitTokens: 1000, Basis: "Tokens",
	})
	if err != nil {
		t.Fatalf("UpsertBudget: %v", err)
	}
	if !got.Created || got.Budget.Basis != "tokens" || got.Budget.Window != "weekly" {
		t.Errorf("budget = %+v (created %v); want a new weekly tokens budget", got.Budget, got.Created)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config invalid after upsert: %v", err)
	}
}

// The limit is denominated in the basis's own unit. A token ceiling under a
// spend basis watches dollars nobody configured, so the mismatch is refused
// before it reaches disk rather than at the next daemon boot.
func TestUpsertBudgetRejectsCeilingInTheWrongUnit(t *testing.T) {
	cases := []struct {
		name string
		u    BudgetUpdate
		want string
	}{
		{"tokens without token basis", BudgetUpdate{Name: "b", LimitTokens: 10}, "limit_usd"},
		{"token basis with only usd", BudgetUpdate{Name: "b", LimitUSD: 10, Basis: "tokens"}, "limit_tokens"},
		{"unknown basis", BudgetUpdate{Name: "b", LimitUSD: 10, Basis: "vibes"}, "basis"},
		{"unknown window", BudgetUpdate{Name: "b", LimitUSD: 10, Window: "hourly"}, "window"},
		{"threshold out of range", BudgetUpdate{Name: "b", LimitUSD: 10, WarnAt: 2}, "warn_at"},
		{"no name", BudgetUpdate{LimitUSD: 10}, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			_, err := c.UpsertBudget(tc.u)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want one naming %q", err, tc.want)
			}
			if len(c.Budgets) != 0 {
				t.Errorf("invalid budget added: %+v", c.Budgets)
			}
		})
	}
}

// A rejected edit must leave the stored budget exactly as it was — the
// caller persists c afterwards only on success, but a half-applied merge
// in memory is still a trap for anything that reads c next.
func TestUpsertBudgetLeavesExistingOnRejection(t *testing.T) {
	c := Default()
	orig := BudgetConfig{Name: "m", Window: "monthly", LimitUSD: 50}
	c.Budgets = []BudgetConfig{orig}
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "m", Basis: "tokens"}); err == nil {
		t.Fatal("switching to basis tokens without limit_tokens was accepted")
	}
	if c.Budgets[0] != orig {
		t.Errorf("budget mutated by a rejected edit: %+v", c.Budgets[0])
	}
}

// A new budget with no window falls to monthly, the terminal's default,
// so the two surfaces create the same budget from the same inputs.
func TestUpsertBudgetDefaultsWindowForNewBudget(t *testing.T) {
	c := Default()
	got, err := c.UpsertBudget(BudgetUpdate{Name: "m", LimitUSD: 50})
	if err != nil {
		t.Fatalf("UpsertBudget: %v", err)
	}
	if got.Budget.Window != DefaultBudgetWindow {
		t.Errorf("window = %q; want %q", got.Budget.Window, DefaultBudgetWindow)
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]string{"passive": "passive", "ACTIVE": "active", " Active ": "active"} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "turbo", "on"} {
		if _, err := ParseMode(in); err == nil {
			t.Errorf("ParseMode(%q) accepted", in)
		}
	}
}

func TestRemoveBudget(t *testing.T) {
	c := Default()
	c.Budgets = []BudgetConfig{{Name: "a"}, {Name: "b"}}
	if !c.RemoveBudget("a") {
		t.Fatal("RemoveBudget(a) = false")
	}
	if c.RemoveBudget("a") {
		t.Error("RemoveBudget(a) twice = true")
	}
	if len(c.Budgets) != 1 || c.Budgets[0].Name != "b" {
		t.Errorf("budgets = %+v", c.Budgets)
	}
}
