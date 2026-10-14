package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func runBudgetSet(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := withConfigFlag(newBudgetSetCmd())
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

	out, err := runBudgetSet(t, "--config", path, "weekly-tokens", "--warn-at", "0.6")
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
	if len(cfg.Budgets) != 1 || !reflect.DeepEqual(cfg.Budgets[0], want) {
		t.Errorf("budgets = %+v; want [%+v]", cfg.Budgets, want)
	}
}

func TestBudgetSetNewBudgetNeedsACeiling(t *testing.T) {
	path := seedConfig(t)
	_, err := runBudgetSet(t, "--config", path, "nothing")
	if err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("err = %v; want a missing-ceiling refusal", err)
	}
}

func runBudgetCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := withConfigFlag(newBudgetCmd())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// $10 a day hard and $100 a month soft, from the terminal: added, listed
// with each window's mode, and one window removed again.
func TestBudgetWindowsFromTheTerminal(t *testing.T) {
	path := seedConfig(t)
	steps := [][]string{
		{"set", "team", "--limit-usd", "100", "--config", path, "--no-restart"},
		{"set", "team", "--add-window", "--window", "daily", "--limit-usd", "10", "--mode", "hard", "--config", path, "--no-restart"},
	}
	for _, args := range steps {
		if out, err := runBudgetCmd(t, args...); err != nil {
			t.Fatalf("budget %v: %v\n%s", args, err, out)
		}
	}
	out, err := runBudgetCmd(t, "list", "--config", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"team", "monthly", "100.00 USD", "soft",
		"daily", "10.00 USD", "hard (shadow: tokenops boundaries promote budget-team-daily)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if out, err := runBudgetCmd(t, "unset", "team", "--window", "daily", "--config", path, "--no-restart"); err != nil || !strings.Contains(out, "removed the daily window") {
		t.Fatalf("unset window: %v\n%s", err, out)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].Window != "monthly" || len(cfg.Budgets[0].Windows) != 0 {
		t.Errorf("budgets = %+v; want the monthly window alone", cfg.Budgets)
	}
}

func TestBudgetSetNewBudgetDefaultsToMonthly(t *testing.T) {
	path := seedConfig(t)
	if out, err := runBudgetSet(t, "--config", path, "m", "--limit-usd", "50"); err != nil {
		t.Fatalf("budget set: %v\n%s", err, out)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].Window != "monthly" {
		t.Errorf("budgets = %+v; want one monthly budget", cfg.Budgets)
	}
}
