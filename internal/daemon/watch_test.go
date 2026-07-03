package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// watchTick must log a budget alert when window spend crosses the warn
// threshold, an unpriced-model warning for catalog misses, and dedupe
// repeats across ticks.
func TestWatchTickAlertsAndDedupes(t *testing.T) {
	// Earlier daemon tests (RunWithLogger) install a process-global
	// domain bus in the budget package; after their shutdown publishing
	// to it blocks forever. Detach so this test stands alone.
	budget.SetDomainBus(nil)

	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now().UTC()
	envs := []*eventschema.Envelope{
		{
			ID: "costly", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: now.Add(-time.Hour), Source: "proxy",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-fable-5",
				InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100,
				CostUSD: 90, // 90% of the $100 budget → warn
			},
		},
		{
			ID: "mystery", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: now.Add(-30 * time.Minute), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-unreleased-9",
				InputTokens: 500, OutputTokens: 50, TotalTokens: 550,
			},
		},
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}

	spendEng := spend.NewEngine(spend.DefaultTable())
	agg := analytics.New(store, spendEng)
	limits := []budget.Limit{{Name: "daily-all", Window: budget.WindowDaily, LimitUSD: 100}}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	seen := map[string]bool{}

	watchTick(ctx, agg, spendEng, limits, seen, logger)
	out := buf.String()
	if !strings.Contains(out, "budget alert") || !strings.Contains(out, "daily-all") {
		t.Errorf("missing budget alert in output:\n%s", out)
	}
	if !strings.Contains(out, "unpriced model") || !strings.Contains(out, "claude-unreleased-9") {
		t.Errorf("missing unpriced-model warning in output:\n%s", out)
	}

	// Second tick: nothing new, nothing re-logged.
	buf.Reset()
	watchTick(ctx, agg, spendEng, limits, seen, logger)
	if got := buf.String(); strings.Contains(got, "budget alert") || strings.Contains(got, "unpriced model") {
		t.Errorf("alerts re-logged on unchanged state:\n%s", got)
	}
}

func TestPlanCostSource(t *testing.T) {
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	if got := planCostSource(cfg, eventschema.ProviderAnthropic); got != eventschema.CostSourcePlanIncluded {
		t.Errorf("anthropic = %q; want plan_included", got)
	}
	if got := planCostSource(cfg, eventschema.ProviderOpenAI); got != "" {
		t.Errorf("openai (no plan) = %q; want empty", got)
	}
}
