package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A provider-scoped budget names a provider the registry knows; an unknown
// one is refused with the ids it was probably meant to be.
func TestBudgetProviderScopeIsValidatedAgainstTheRegistry(t *testing.T) {
	b := BudgetConfig{Name: "mine", Window: "monthly", LimitUSD: 50, Provider: "fireworks"}
	if err := b.Validate(); err != nil {
		t.Fatalf("known provider refused: %v", err)
	}
	b.Provider = "fireworx"
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown provider") || !strings.Contains(err.Error(), "fireworks") {
		t.Fatalf("Validate = %v, want an unknown-provider error suggesting fireworks", err)
	}
	b.Provider = "zzzzqqqq"
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "tokenops provider list") {
		t.Errorf("Validate = %v, want a pointer to the provider list when nothing is close", err)
	}
}

// The upsert stores the provider's canonical id, so `--provider Fireworks`
// and `--provider fireworks` are one budget scope; an edit that names no
// provider keeps it.
func TestUpsertBudgetStoresTheCanonicalProviderAndKeepsItOnEdit(t *testing.T) {
	var c Config
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "mine", LimitUSD: 50, Provider: " Fireworks "}); err != nil {
		t.Fatal(err)
	}
	if got := c.Budgets[0].Provider; got != "fireworks" {
		t.Fatalf("stored provider = %q, want fireworks", got)
	}
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "mine", WarnAt: 0.5}); err != nil {
		t.Fatal(err)
	}
	if got := c.Budgets[0].Provider; got != "fireworks" {
		t.Errorf("an edit dropped the provider: %q", got)
	}
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "mine", Provider: "nope-not-real"}); err == nil {
		t.Error("an unknown provider was stored")
	}
	if got := c.Budgets[0].Provider; got != "fireworks" {
		t.Errorf("a refused edit changed the budget: %q", got)
	}
}

// Every window of a provider-scoped budget is measured for that provider,
// beside whatever other scope it has: the scopes narrow together.
func TestBudgetLimitsCarryTheProviderToEveryWindow(t *testing.T) {
	var c Config
	c.Budgets = []BudgetConfig{{Name: "mine", Provider: "fireworks", Tag: "client=acme-test", WorkflowID: "wf",
		Windows: []BudgetWindowConfig{{Window: "daily", LimitUSD: 5}, {Window: "monthly", LimitUSD: 50, Mode: "hard"}}}}
	limits := c.BudgetLimits()
	if len(limits) != 2 {
		t.Fatalf("limits = %+v", limits)
	}
	for _, l := range limits {
		if l.Provider != "fireworks" || l.Tag != "client=acme-test" || l.WorkflowID != "wf" {
			t.Errorf("limit %s lost a scope: %+v", l.Key(), l)
		}
	}
	if rules := c.BudgetRules(); len(rules) != 1 {
		t.Errorf("hard window rules = %+v, want one", rules)
	}
}

// Budgets written before provider scopes load as they were — all spend —
// and are written back without a provider key.
func TestBudgetsWithoutAProviderAreUnchanged(t *testing.T) {
	const before = `budgets:
  - name: team
    window: monthly
    limit_usd: 100
    limit_tokens: 0
    warn_at: 0
    crit_at: 0
    workflow_id: ""
    agent_id: ""
    basis: ""
  - name: tok
    limit_usd: 0
    limit_tokens: 0
    warn_at: 0
    crit_at: 0
    workflow_id: ""
    agent_id: ""
    basis: tokens
    windows:
      - window: daily
        limit_tokens: 1000
        mode: hard
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("an existing budget no longer loads: %v", err)
	}
	for _, l := range c.BudgetLimits() {
		if l.Provider != "" {
			t.Errorf("limit %s gained a provider: %+v", l.Key(), l)
		}
	}
	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(struct {
		Budgets []BudgetConfig `yaml:"budgets"`
	}{c.Budgets}); err != nil {
		t.Fatal(err)
	}
	if out.String() != before {
		t.Errorf("budgets written back differently:\n%s\nwant:\n%s", out.String(), before)
	}
}
