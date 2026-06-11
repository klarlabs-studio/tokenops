package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() invalid: %v", err)
	}
}

func TestLoadAppliesYAMLAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const yaml = `
listen: "0.0.0.0:9090"
log:
  level: debug
  format: json
shutdown:
  timeout: 5s
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9090" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v", cfg.Log)
	}
	if cfg.Shutdown.Timeout != 5*time.Second {
		t.Errorf("Shutdown.Timeout = %s", cfg.Shutdown.Timeout)
	}
}

func TestLoadEnvOverridesWinOverFile(t *testing.T) {
	t.Setenv("TOKENOPS_LISTEN", "127.0.0.1:1234")
	t.Setenv("TOKENOPS_LOG_LEVEL", "warn")
	t.Setenv("TOKENOPS_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:1234" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("Level = %q", cfg.Log.Level)
	}
	if cfg.Shutdown.Timeout != 30*time.Second {
		t.Errorf("Timeout = %s", cfg.Shutdown.Timeout)
	}
}

func TestLoadShutdownTimeoutIntegerSeconds(t *testing.T) {
	t.Setenv("TOKENOPS_SHUTDOWN_TIMEOUT", "7")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Shutdown.Timeout != 7*time.Second {
		t.Errorf("Timeout = %s", cfg.Shutdown.Timeout)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := map[string]Config{
		"empty listen": {Listen: "", Log: LogConfig{Level: "info", Format: "text"}, Shutdown: ShutdownConfig{Timeout: time.Second}},
		"bad level":    {Listen: ":1", Log: LogConfig{Level: "verbose", Format: "text"}, Shutdown: ShutdownConfig{Timeout: time.Second}},
		"bad format":   {Listen: ":1", Log: LogConfig{Level: "info", Format: "xml"}, Shutdown: ShutdownConfig{Timeout: time.Second}},
		"zero timeout": {Listen: ":1", Log: LogConfig{Level: "info", Format: "text"}, Shutdown: ShutdownConfig{Timeout: 0}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadMissingFileErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEnvBoolOverrides(t *testing.T) {
	t.Run("TLS enabled", func(t *testing.T) {
		t.Setenv("TOKENOPS_TLS_ENABLED", "1")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.TLS.Enabled {
			t.Error("TLS.Enabled should be true")
		}
	})
	t.Run("TLS disabled", func(t *testing.T) {
		t.Setenv("TOKENOPS_TLS_ENABLED", "0")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TLS.Enabled {
			t.Error("TLS.Enabled should be false")
		}
	})
	t.Run("storage enabled true", func(t *testing.T) {
		t.Setenv("TOKENOPS_STORAGE_ENABLED", "true")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Storage.Enabled {
			t.Error("Storage.Enabled should be true")
		}
	})
	t.Run("storage enabled false", func(t *testing.T) {
		t.Setenv("TOKENOPS_STORAGE_ENABLED", "false")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Storage.Enabled {
			t.Error("Storage.Enabled should be false")
		}
	})
	t.Run("otel enabled on", func(t *testing.T) {
		t.Setenv("TOKENOPS_OTEL_ENABLED", "on")
		t.Setenv("TOKENOPS_OTEL_ENDPOINT", "http://localhost:4318")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.OTel.Enabled {
			t.Error("OTel.Enabled should be true")
		}
	})
	t.Run("otel enabled off", func(t *testing.T) {
		t.Setenv("TOKENOPS_OTEL_ENABLED", "off")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OTel.Enabled {
			t.Error("OTel.Enabled should be false")
		}
	})
}

func TestRedactEnabledDefaultsTrue(t *testing.T) {
	var o OTelConfig
	if !o.RedactEnabled() {
		t.Error("RedactEnabled should default to true")
	}
}

func TestRedactEnabledExplicitFalse(t *testing.T) {
	v := false
	o := OTelConfig{Redact: &v}
	if o.RedactEnabled() {
		t.Error("RedactEnabled should be false when Redact = &false")
	}
}

func TestRedactEnabledExplicitTrue(t *testing.T) {
	v := true
	o := OTelConfig{Redact: &v}
	if !o.RedactEnabled() {
		t.Error("RedactEnabled should be true when Redact = &true")
	}
}

func TestBlockersOnFreshInstall(t *testing.T) {
	cfg := Default()
	got := cfg.Blockers()
	want := []string{"storage_disabled", "rules_disabled", "providers_unconfigured"}
	if len(got) != len(want) {
		t.Fatalf("blockers=%v want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("blockers[%d]=%q want %q", i, got[i], w)
		}
	}
}

func TestBlockersOnFullyConfigured(t *testing.T) {
	cfg := Default()
	cfg.Storage.Enabled = true
	cfg.Rules.Enabled = true
	cfg.Providers = map[string]string{"anthropic": "https://api.anthropic.com"}
	got := cfg.Blockers()
	if len(got) != 0 {
		t.Errorf("expected no blockers, got %v", got)
	}
}

func TestNextActionsDeduplicatesInitHint(t *testing.T) {
	got := NextActionsFor([]string{"storage_disabled", "rules_disabled"})
	if len(got) != 1 {
		t.Fatalf("expected 1 dedup'd action, got %v", got)
	}
	if got[0] != "run `tokenops init` then restart the daemon" {
		t.Errorf("unexpected action: %q", got[0])
	}
}

func TestNextActionsEmptyWhenNoBlockers(t *testing.T) {
	got := NextActionsFor(nil)
	if got == nil {
		t.Error("expected non-nil empty slice so JSON serialises as []")
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestValidateAcceptsKnownPlan(t *testing.T) {
	cfg := Default()
	cfg.Plans = map[string]string{"anthropic": "claude-max-20x"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate rejected known plan: %v", err)
	}
}

func TestValidateRejectsUnknownPlan(t *testing.T) {
	cfg := Default()
	cfg.Plans = map[string]string{"anthropic": "claude-maxx"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for unknown plan")
	}
	if !strings.Contains(err.Error(), "plans[anthropic]") {
		t.Errorf("error should name the offending provider key: %v", err)
	}
	if !strings.Contains(err.Error(), "claude-max-5x") {
		t.Errorf("error should suggest valid plans: %v", err)
	}
}

func TestEnvOverrideSetsPlan(t *testing.T) {
	t.Setenv("TOKENOPS_PLAN_ANTHROPIC", "claude-pro")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Plans["anthropic"] != "claude-pro" {
		t.Errorf("plans[anthropic]=%q want claude-pro", cfg.Plans["anthropic"])
	}
}

func TestValidateRoutingRules(t *testing.T) {
	cfg := Default()
	cfg.Optimizer.RoutingRules = []RoutingRuleConfig{{
		Provider: "anthropic", FromModel: "claude-fable-5*", ToModel: "claude-opus-4-8", Quality: 0.9,
	}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid routing rule rejected: %v", err)
	}

	cfg.Optimizer.RoutingRules[0].ToModel = ""
	if err := cfg.Validate(); err == nil {
		t.Error("missing to_model accepted")
	}

	cfg.Optimizer.RoutingRules[0].ToModel = "claude-opus-4-8"
	cfg.Optimizer.RoutingRules[0].Quality = 1.5
	if err := cfg.Validate(); err == nil {
		t.Error("quality > 1 accepted")
	}

	cfg.Optimizer.RoutingRules = nil
	cfg.Optimizer.RoutingMinQuality = -0.1
	if err := cfg.Validate(); err == nil {
		t.Error("negative routing_min_quality accepted")
	}
}

func TestValidateContextLimits(t *testing.T) {
	cfg := Default()
	cfg.Coaching.ContextLimits = []ContextLimitConfig{{
		WorkflowPrefix: "claude-code:", MaxContextTokens: 500_000,
	}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid context limit rejected: %v", err)
	}
	cfg.Coaching.ContextLimits[0].WorkflowPrefix = ""
	if err := cfg.Validate(); err == nil {
		t.Error("missing workflow_prefix accepted")
	}
	cfg.Coaching.ContextLimits[0].WorkflowPrefix = "x:"
	cfg.Coaching.ContextLimits[0].MaxContextTokens = -1
	if err := cfg.Validate(); err == nil {
		t.Error("negative threshold accepted")
	}
}

func TestWasteConfigMapsContextLimits(t *testing.T) {
	c := CoachingConfig{ContextLimits: []ContextLimitConfig{{
		WorkflowPrefix: "claude-code:", MaxContextTokens: 500_000, ContextGrowthLimitTokens: 1_000_000,
	}}}
	wc := c.WasteConfig()
	if len(wc.Profiles) != 1 {
		t.Fatalf("profiles = %d; want 1", len(wc.Profiles))
	}
	p := wc.Profiles[0]
	if p.WorkflowPrefix != "claude-code:" || p.MaxContextTokens != 500_000 || p.ContextGrowthLimitTokens != 1_000_000 {
		t.Errorf("profile = %+v", p)
	}
}

func TestValidateMode(t *testing.T) {
	cfg := Default()
	for _, m := range []string{"", "passive", "active", "Active"} {
		cfg.Mode = m
		if err := cfg.Validate(); err != nil {
			t.Errorf("mode %q rejected: %v", m, err)
		}
	}
	cfg.Mode = "aggressive"
	if err := cfg.Validate(); err == nil {
		t.Error("invalid mode accepted")
	}
	cfg.Mode = "active"
	if !cfg.ActiveMode() {
		t.Error("ActiveMode() false for active")
	}
	cfg.Mode = ""
	if cfg.ActiveMode() {
		t.Error("ActiveMode() true for empty mode")
	}
}

func TestValidateBudgets(t *testing.T) {
	cfg := Default()
	cfg.Budgets = []BudgetConfig{{Name: "weekly-all", Window: "weekly", LimitUSD: 50}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid budget rejected: %v", err)
	}
	cfg.Budgets[0].Window = "fortnightly"
	if err := cfg.Validate(); err == nil {
		t.Error("invalid window accepted")
	}
	cfg.Budgets[0].Window = "weekly"
	cfg.Budgets[0].LimitUSD = 0
	if err := cfg.Validate(); err == nil {
		t.Error("zero limit accepted")
	}
	cfg.Budgets[0].LimitUSD = 50
	cfg.Budgets[0].Name = ""
	if err := cfg.Validate(); err == nil {
		t.Error("missing name accepted")
	}
}

func TestWatchEffectiveInterval(t *testing.T) {
	if got := (WatchConfig{}).EffectiveInterval(); got != 15*time.Minute {
		t.Errorf("default interval = %s", got)
	}
	if got := (WatchConfig{Interval: time.Second}).EffectiveInterval(); got != time.Minute {
		t.Errorf("sub-minute interval not clamped: %s", got)
	}
	if got := (WatchConfig{Interval: time.Hour}).EffectiveInterval(); got != time.Hour {
		t.Errorf("explicit interval altered: %s", got)
	}
}

func TestBudgetLimitsMapping(t *testing.T) {
	cfg := Config{Budgets: []BudgetConfig{{
		Name: "eng", Window: "Monthly", LimitUSD: 200, WarnAt: 0.5, WorkflowID: "wf-1",
	}}}
	limits := cfg.BudgetLimits()
	if len(limits) != 1 {
		t.Fatalf("limits = %d", len(limits))
	}
	l := limits[0]
	if l.Name != "eng" || l.Window != "monthly" || l.LimitUSD != 200 || l.WarnAt != 0.5 || l.WorkflowID != "wf-1" {
		t.Errorf("limit = %+v", l)
	}
}

func TestValidateBudgetBasis(t *testing.T) {
	cfg := Default()
	cfg.Budgets = []BudgetConfig{{Name: "w", Window: "weekly", LimitUSD: 10, Basis: "equivalent"}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("equivalent basis rejected: %v", err)
	}
	cfg.Budgets[0].Basis = "shadow"
	if err := cfg.Validate(); err == nil {
		t.Error("invalid basis accepted")
	}
}
