package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// Binding a spend-denominated plan without the org limit must fail. The
// alternative is a headroom percentage computed against a default nobody
// chose, which reads exactly as authoritative as a real one.
func TestPlanSetRefusesEnterpriseWithoutASpendLimit(t *testing.T) {
	path := seedConfig(t)
	cmd := newPlanSetCmd()
	cmd.SetArgs([]string{"anthropic", "claude-enterprise", "--config-path", path})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "--spend-limit") {
		t.Errorf("the refusal should name the flag to supply: %v", err)
	}
}

func TestPlanSetStoresTheSpendLimit(t *testing.T) {
	path := seedConfig(t)
	cmd := newPlanSetCmd()
	cmd.SetArgs([]string{
		"anthropic", "claude-enterprise",
		"--spend-limit", "5000", "--limit-window", "monthly", "--rate-factor", "0.8",
		"--no-restart", "--config-path", path,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cfg.Plans["anthropic"] != "claude-enterprise" {
		t.Errorf("plan = %q", cfg.Plans["anthropic"])
	}
	lim := cfg.PlanLimits["anthropic"]
	if lim.SpendLimitUSD != 5000 || lim.Window != "monthly" || lim.RateFactor != 0.8 {
		t.Errorf("plan_limits = %+v", lim)
	}
}

// A windowed plan takes no spend limit and must not acquire one by accident.
func TestPlanSetLeavesWindowedPlansAlone(t *testing.T) {
	path := seedConfig(t)
	cmd := newPlanSetCmd()
	cmd.SetArgs([]string{"anthropic", "claude-max-20x", "--no-restart", "--config-path", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.PlanLimits) != 0 {
		t.Errorf("plan_limits should stay empty: %+v", cfg.PlanLimits)
	}
}
