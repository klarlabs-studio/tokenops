package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// GET /api/coach carries the current Claude Code session's cache
// countdown beside the coach's dials.
func TestCoachAPICarriesThePromptCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(home, "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.Append(ctx, &eventschema.Envelope{
		ID: "cc-1", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: now.Add(-4 * time.Minute), Source: "claude-code-jsonl",
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, SessionID: "s", RequestModel: "claude-opus-4-7",
			InputTokens: 10_000, CachedInputTokens: 9_000, CacheWriteInputTokens: 1_000},
	}); err != nil {
		t.Fatal(err)
	}
	d := stateDeps(config.Config{}, store, nil, nil)()
	r := d.Coach(now)
	if r.PromptCache == nil || r.PromptCache.State != waste.CacheExpiring {
		t.Fatalf("prompt cache = %+v", r.PromptCache)
	}
	if d := stateDeps(config.Config{}, nil, nil, nil)(); d.Coach(now).PromptCache != nil {
		t.Error("a countdown without storage")
	}
}
