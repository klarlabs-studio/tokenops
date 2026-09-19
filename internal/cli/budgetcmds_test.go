package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func runBudgetSet(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newBudgetSetCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(append(args, "--no-restart"))
	err := cmd.Execute()
	return out.String(), err
}

// `budget set` and tokenops_budget_set share one upsert. Editing one field
// of an existing budget must keep the rest — including a window the edit
// did not name, which the flag's old "monthly" default used to overwrite.
func TestBudgetSetEditKeepsUnnamedFields(t *testing.T) {
	path := seedConfig(t)
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	cfg.Budgets = []config.BudgetConfig{{
		Name: "weekly-tokens", Window: "weekly", LimitTokens: 15_000_000_000, Basis: "tokens",
	}}
	if err := config.WriteMutable(path, cfg); err != nil {
		t.Fatalf("seed budget: %v", err)
	}

	out, err := runBudgetSet(t, "--config-path", path, "weekly-tokens", "--warn-at", "0.6")
	if err != nil {
		t.Fatalf("budget set: %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated budget") {
		t.Errorf("output should report an update: %s", out)
	}
	cfg, _ = config.ReadMutable(path)
	want := config.BudgetConfig{
		Name: "weekly-tokens", Window: "weekly", LimitTokens: 15_000_000_000, Basis: "tokens", WarnAt: 0.6,
	}
	if len(cfg.Budgets) != 1 || cfg.Budgets[0] != want {
		t.Errorf("budgets = %+v; want [%+v]", cfg.Budgets, want)
	}
}

func TestBudgetSetNewBudgetNeedsACeiling(t *testing.T) {
	path := seedConfig(t)
	_, err := runBudgetSet(t, "--config-path", path, "nothing")
	if err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("err = %v; want a missing-ceiling refusal", err)
	}
}

func TestBudgetSetNewBudgetDefaultsToMonthly(t *testing.T) {
	path := seedConfig(t)
	if out, err := runBudgetSet(t, "--config-path", path, "m", "--limit-usd", "50"); err != nil {
		t.Fatalf("budget set: %v\n%s", err, out)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].Window != "monthly" {
		t.Errorf("budgets = %+v; want one monthly budget", cfg.Budgets)
	}
}
