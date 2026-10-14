package actions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// sandbox gives the test its own HOME and a config file, so plan history
// and the event store never land in the operator's.
func sandbox(t *testing.T, initial string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".tokenops"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func reload(t *testing.T, path string) config.Config {
	t.Helper()
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSetMode(t *testing.T) {
	path := sandbox(t, "log:\n  level: info\n")
	got, err := SetMode(path, "active")
	if err != nil || got.Mode != "active" || !got.Active {
		t.Fatalf("got %+v, %v", got, err)
	}
	if reload(t, path).Mode != "active" {
		t.Error("mode not written")
	}
	if _, err := SetMode(path, "turbo"); !IsInput(err) {
		t.Errorf("bad mode err = %v, want an input error", err)
	}
}

func TestSetBudgetUpsertsAndDeletes(t *testing.T) {
	path := sandbox(t, "log:\n  level: info\n")
	got, err := SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "monthly", LimitUSD: 50}})
	if err != nil || len(got.Budgets) != 1 {
		t.Fatalf("create = %+v, %v", got, err)
	}
	if _, err := SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "nope"}, Delete: true}); !IsInput(err) {
		t.Errorf("delete missing err = %v", err)
	}
	if got, err := SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "monthly"}, Delete: true}); err != nil || len(got.Budgets) != 0 {
		t.Errorf("delete = %+v, %v", got, err)
	}
}

func TestSetBudgetAddsAndRemovesWindows(t *testing.T) {
	path := sandbox(t, "log:\n  level: info\n")
	if _, err := SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "team", LimitUSD: 100}}); err != nil {
		t.Fatal(err)
	}
	got, err := SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "team", Window: "daily", LimitUSD: 10, AddWindow: true}})
	if err != nil || len(got.Budgets) != 1 || len(got.Budgets[0].Windows) != 2 {
		t.Fatalf("add window = %+v, %v", got, err)
	}
	if _, err := SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "team"}, RemoveWindow: "weekly"}); !IsInput(err) {
		t.Errorf("removing a missing window err = %v", err)
	}
	got, err = SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "team"}, RemoveWindow: "daily"})
	if err != nil || got.Budgets[0].Window != "monthly" || len(got.Budgets[0].Windows) != 0 {
		t.Fatalf("remove window = %+v, %v", got, err)
	}
}

// The API and MCP tool are reachable by the agent a hard window limits:
// they may make a budget hard (it starts in shadow), but not loosen or
// remove one that is.
func TestSetBudgetLeavesHardWindowsToTheTerminal(t *testing.T) {
	path := sandbox(t, "log:\n  level: info\n")
	got, err := SetBudget(path, BudgetRequest{BudgetUpdate: config.BudgetUpdate{Name: "team", Window: "daily", LimitUSD: 10, Mode: "hard"}})
	if err != nil || got.Budgets[0].Mode != "hard" {
		t.Fatalf("making a budget hard = %+v, %v", got, err)
	}
	for name, req := range map[string]BudgetRequest{
		"soften":        {BudgetUpdate: config.BudgetUpdate{Name: "team", Mode: "soft"}},
		"raise":         {BudgetUpdate: config.BudgetUpdate{Name: "team", LimitUSD: 1000}},
		"delete":        {BudgetUpdate: config.BudgetUpdate{Name: "team"}, Delete: true},
		"rescope":       {BudgetUpdate: config.BudgetUpdate{Name: "team", WorkflowID: "elsewhere"}},
		"remove window": {BudgetUpdate: config.BudgetUpdate{Name: "team"}, RemoveWindow: "daily"},
	} {
		if _, err := SetBudget(path, req); !IsInput(err) || !strings.Contains(err.Error(), "terminal") {
			t.Errorf("%s: err = %v; want it refused, pointing at the terminal", name, err)
		}
	}
	if b := reload(t, path).Budgets[0]; b.Mode != "hard" || b.LimitUSD != 10 {
		t.Errorf("budget changed: %+v", b)
	}
}

func TestSetRoutingRuleValidatesBeforeWriting(t *testing.T) {
	path := sandbox(t, "log:\n  level: info\n")
	before, _ := os.ReadFile(path)
	if _, err := SetRoutingRule(path, RoutingRuleRequest{Provider: "anthropic"}); !IsInput(err) {
		t.Fatalf("invalid rule err = %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("a refused rule touched the file")
	}
	if _, err := SetRoutingRule(path, RoutingRuleRequest{Provider: "anthropic", FromModel: "x", Delete: true}); !IsInput(err) {
		t.Errorf("delete missing err = %v", err)
	}
}

func TestSetPlanBindsRecordsAndClears(t *testing.T) {
	path := sandbox(t, "plans:\n  openai: gpt-plus\n")
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	got, err := SetPlan(context.Background(), path, PlanRequest{Provider: "openai", Plan: "gpt-pro-5x", Actor: "api"}, now)
	if err != nil || got.Plan != "gpt-pro-5x" || got.Previous != "gpt-plus" || got.HistoryError != "" {
		t.Fatalf("bind = %+v, %v", got, err)
	}
	if reload(t, path).Plans["openai"] != "gpt-pro-5x" {
		t.Error("binding not written")
	}
	if got, err := SetPlan(context.Background(), path, PlanRequest{Provider: "openai", Clear: true}, now); err != nil || !got.Cleared {
		t.Errorf("clear = %+v, %v", got, err)
	}
	if _, err := SetPlan(context.Background(), path, PlanRequest{}, now); !IsInput(err) {
		t.Errorf("no provider err = %v", err)
	}
	if _, err := SetPlan(context.Background(), path, PlanRequest{Provider: "openai", Plan: "gpt-pro-5x", Since: "last week"}, now); !IsInput(err) {
		t.Errorf("bad since err = %v", err)
	}
}
