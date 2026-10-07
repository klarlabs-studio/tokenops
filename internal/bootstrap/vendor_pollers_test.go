package bootstrap

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestPlanCostSource(t *testing.T) {
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	if got := PlanCostSource(cfg, eventschema.ProviderAnthropic); got != eventschema.CostSourcePlanIncluded {
		t.Errorf("anthropic = %q; want plan_included", got)
	}
	if got := PlanCostSource(cfg, eventschema.ProviderOpenAI); got != "" {
		t.Errorf("openai (no plan) = %q; want empty", got)
	}
}

// The clearance cookie expires within hours, and claude.ai expires the
// session itself every few weeks: a meter that cannot re-read the browser
// stops, as a pasted one did for 20 hours on 2026-10-05. Every meter gets
// the browser as its way back (ADR 0011) unless browser: none says not to.
func TestBrowserSessionSourceFollowsTheConfig(t *testing.T) {
	if browserSessionSource(config.ClaudeUsageMeterConfig{}) == nil {
		t.Error("a pasted session has no way back when claude.ai refuses it")
	}
	if browserSessionSource(config.ClaudeUsageMeterConfig{FromBrowser: true}) == nil {
		t.Error("from_browser set, but the poller got no way to re-read the session")
	}
	if browserSessionSource(config.ClaudeUsageMeterConfig{Browser: "None"}) != nil {
		t.Error("browser: none still reads a browser")
	}
}

// With every configurable reader off, only the always-on readers run:
// the Cursor hook ledger and the Claude Code status-line file, both of
// which cost nothing when their file does not exist.
func TestStartVendorUsagePollersStartsOnlyAlwaysOnReaders(t *testing.T) {
	sandbox := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, sandbox)
	}
	off := false
	cfg := config.Config{VendorUsage: config.VendorUsageConfig{
		CodexAppServer: config.CodexAppServerConfig{Enabled: &off},
		Fireworks:      config.FireworksUsageConfig{Enabled: &off},
		Accounts:       config.AccountsUsageConfig{Enabled: &off},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.DiscardHandler)
	sup := lifecycle.New(ctx, logger)
	bus := events.NewAsync(events.NoopSink{}, events.Options{})
	StartVendorUsagePollers(cfg, bus, freshness.NewRegistry(), func(time.Time) string { return "" }, sup, logger)

	got := sup.Running()
	slices.Sort(got)
	if want := []string{"claude-code-statusline", "cursor-hook"}; !slices.Equal(got, want) {
		t.Errorf("running = %v, want %v", got, want)
	}
	cancel()
	if err := sup.Wait(5 * time.Second); err != nil {
		t.Errorf("pollers did not stop: %v", err)
	}
	_ = bus.Close(time.Second)
}
