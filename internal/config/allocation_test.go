package config

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAllocationRulesReadFromYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var c Config
	err := yaml.Unmarshal([]byte(`
allocation:
  rules:
    - dir: ~/clients/acme
      tags: {project: storefront, client: Acme Corp}
    - repo: "acme/*"
      branch_prefix: feat/
      harness: codex
      tags: {cost_center: cc-42}
`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Allocation.Validate(); err != nil {
		t.Fatalf("valid rules refused: %v", err)
	}
	rules := c.AllocationRules()
	if len(rules) != 2 {
		t.Fatalf("rules = %+v", rules)
	}
	if want := filepath.Join(home, "clients", "acme"); rules[0].Match.Dir != want {
		t.Errorf("dir = %q, want %q", rules[0].Match.Dir, want)
	}
	if rules[0].Tags.Client != "Acme Corp" || rules[1].Tags.CostCenter != "cc-42" || rules[1].Match.Harness != "codex" {
		t.Errorf("rules = %+v", rules)
	}
}

func TestConfigValidateRefusesABadAllocationRule(t *testing.T) {
	c := Default()
	c.Allocation.Rules = []AllocationRule{{Dir: "relative/path", Tags: AllocationTags{Client: "acme"}}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "allocation rule 1") {
		t.Fatalf("Validate = %v", err)
	}
}

func TestBudgetTagScope(t *testing.T) {
	b := BudgetConfig{Name: "acme", Window: "monthly", LimitUSD: 50, Tag: "client=Acme Corp"}
	if err := b.Validate(); err != nil {
		t.Fatalf("valid tag refused: %v", err)
	}
	b.Tag = "team=x"
	if err := b.Validate(); err == nil || !strings.Contains(err.Error(), "tag") {
		t.Errorf("Validate = %v, want a tag error", err)
	}
	c := Config{Budgets: []BudgetConfig{{Name: "acme", Window: "monthly", LimitUSD: 50, Tag: "cost-center=cc-42"}}}
	if got := c.BudgetLimits()[0].Tag; got != "cost_center=cc-42" {
		t.Errorf("limit tag = %q, want it normalized", got)
	}
	var cfg Config
	if _, err := cfg.UpsertBudget(BudgetUpdate{Name: "x", LimitUSD: 5, Tag: "project=web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.UpsertBudget(BudgetUpdate{Name: "x", WarnAt: 0.5}); err != nil {
		t.Fatal(err)
	}
	if cfg.Budgets[0].Tag != "project=web" {
		t.Errorf("an edit dropped the tag: %+v", cfg.Budgets[0])
	}
}
