package mcp

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

func newModeServer(t *testing.T) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.WriteMutable(path, config.Default()); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterModeTools(srv, ModeDeps{ConfigPath: path}); err != nil {
		t.Fatalf("RegisterModeTools: %v", err)
	}
	return srv, path
}

func TestModeToolGetAndSet(t *testing.T) {
	srv, path := newModeServer(t)

	out := execTool(t, srv, "tokenops_mode", nil)
	if !strings.Contains(out, `"mode": "passive"`) {
		t.Errorf("default mode not passive: %s", out)
	}

	out = execTool(t, srv, "tokenops_mode", map[string]any{"set": "active"})
	if !strings.Contains(out, `"mode": "active"`) || !strings.Contains(out, "restart") {
		t.Errorf("set output: %s", out)
	}

	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !cfg.ActiveMode() {
		t.Error("mode not persisted to disk")
	}
}

func TestBudgetSetUpsertAndDelete(t *testing.T) {
	srv, path := newModeServer(t)

	execTool(t, srv, "tokenops_budget_set", map[string]any{
		"name": "weekly-all", "window": "weekly", "limit_usd": 50,
	})
	execTool(t, srv, "tokenops_budget_set", map[string]any{
		"name": "weekly-all", "window": "weekly", "limit_usd": 75, "warn_at": 0.5,
	})
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Budgets) != 1 {
		t.Fatalf("budgets = %d; want 1 after upsert", len(cfg.Budgets))
	}
	if cfg.Budgets[0].LimitUSD != 75 || cfg.Budgets[0].WarnAt != 0.5 {
		t.Errorf("budget = %+v", cfg.Budgets[0])
	}

	execTool(t, srv, "tokenops_budget_set", map[string]any{
		"name": "weekly-all", "delete": true,
	})
	cfg, _ = config.ReadMutable(path)
	if len(cfg.Budgets) != 0 {
		t.Errorf("budget not deleted: %+v", cfg.Budgets)
	}
}

func TestRoutingRuleSetUpsertAndValidation(t *testing.T) {
	srv, path := newModeServer(t)

	execTool(t, srv, "tokenops_routing_rule_set", map[string]any{
		"provider": "anthropic", "from_model": "claude-fable-5*",
		"to_model": "claude-opus-4-8", "quality": 0.9,
	})
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Optimizer.RoutingRules) != 1 {
		t.Fatalf("rules = %d", len(cfg.Optimizer.RoutingRules))
	}
	if cfg.Optimizer.RoutingRules[0].ToModel != "claude-opus-4-8" {
		t.Errorf("rule = %+v", cfg.Optimizer.RoutingRules[0])
	}

	// Invalid quality must be rejected by validation and leave the file
	// untouched.
	tool, _ := srv.GetTool("tokenops_routing_rule_set")
	_, err := tool.Execute(t.Context(), []byte(`{"provider":"anthropic","from_model":"x*","to_model":"y","quality":5}`))
	if err == nil {
		t.Error("quality=5 accepted")
	}
	cfg, _ = config.ReadMutable(path)
	if len(cfg.Optimizer.RoutingRules) != 1 {
		t.Errorf("invalid mutation persisted: %+v", cfg.Optimizer.RoutingRules)
	}
}
