package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Turns Fireworks served through Claude Code were recorded as Anthropic's
// and covered by the Max plan. At start they move to Fireworks and become
// billed; Claude turns stay where they were.
func TestCorrectGatewayAttribution(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.fireworks.ai/inference"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mk := func(id, model string) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: claudecodejsonl.SourceTag,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: model, CostSource: eventschema.CostSourcePlanIncluded},
		}
	}
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{mk("k", "kimi-k3"), mk("c", "claude-sonnet-5")}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	correctGatewayAttribution(ctx, cfg, store, logger)
	correctGatewayAttribution(ctx, cfg, store, logger) // idempotent

	got, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range got {
		p := env.Payload.(*eventschema.PromptEvent)
		switch env.ID {
		case "k":
			if p.Provider != eventschema.ProviderFireworks || p.CostSource != eventschema.CostSourceMetered {
				t.Errorf("kimi-k3: provider %q, cost source %q; want fireworks, billed", p.Provider, p.CostSource)
			}
		case "c":
			if p.Provider != eventschema.ProviderAnthropic || p.CostSource != eventschema.CostSourcePlanIncluded {
				t.Errorf("claude: provider %q, cost source %q; want unchanged", p.Provider, p.CostSource)
			}
		}
	}
	if models, _ := store.ModelsFor(ctx, claudecodejsonl.SourceTag, "fireworks"); len(models) != 1 || models[0] != "kimi-k3" {
		t.Errorf("provider column not moved: fireworks models %v", models)
	}
}
