package actions

import (
	"context"
	"os"
	"path/filepath"
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
