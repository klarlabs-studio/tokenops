package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Before TokenOps knew endpoints, every Claude Code turn was Anthropic's
// and covered by the Max plan. FireConnect pointed Claude Code at
// Fireworks on the 20th. At start: the Fireworks model moves to
// Fireworks; Claude turns after the 20th went through FireRouter on an API
// key and become billed; Claude turns before it stay covered.
func TestCorrectGatewayAttributionFollowsTheRouteHistory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	switched := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	routes, err := routehistory.OpenAt(filepath.Join(home, "route-history.jsonl"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Observe(routehistory.HarnessClaudeCode, "", switched.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Observe(routehistory.HarnessClaudeCode, "https://api.fireworks.ai/inference", switched); err != nil {
		t.Fatal(err)
	}

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	mk := func(id, model string, at time.Time) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: claudecodejsonl.SourceTag,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: model, CostSource: eventschema.CostSourcePlanIncluded},
		}
	}
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{
		mk("kimi", "kimi-k3", switched.Add(time.Hour)),
		mk("claude-before", "claude-sonnet-5", switched.Add(-time.Hour)),
		mk("claude-after", "claude-sonnet-5", switched.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	correctGatewayAttribution(ctx, cfg, store, routes, logger)
	correctGatewayAttribution(ctx, cfg, store, routes, logger) // idempotent

	got, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		provider eventschema.Provider
		source   eventschema.CostSource
		endpoint string
	}{
		"kimi":          {eventschema.ProviderFireworks, eventschema.CostSourceMetered, "fireworks"},
		"claude-before": {eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded, ""},
		"claude-after":  {eventschema.ProviderAnthropic, eventschema.CostSourceMetered, "fireworks"},
	}
	for _, env := range got {
		p := env.Payload.(*eventschema.PromptEvent)
		w := want[env.ID]
		if p.Provider != w.provider || p.CostSource != w.source || env.Attributes["endpoint"] != w.endpoint {
			t.Errorf("%s: provider %q, cost source %q, endpoint %q; want %+v",
				env.ID, p.Provider, p.CostSource, env.Attributes["endpoint"], w)
		}
	}
}
