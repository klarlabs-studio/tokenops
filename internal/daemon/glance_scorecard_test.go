package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The daemon's event cache holds as far back as a scorecard reads, so a
// scorecard's five weeks come from memory and not from the store: held a
// month, the cache passed a scorecard's read through to the store whole.
func TestGlanceCacheHoldsAScorecardsReach(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	from := now.Add(-spending.ScorecardReach)
	for i, id := range []string{"oldest", "newer"} {
		if err := store.Append(ctx, &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: from.Add(time.Duration(i+1) * time.Hour), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-5", InputTokens: 10},
		}); err != nil {
			t.Fatal(err)
		}
	}
	cache := newGlanceEvents(store)
	if _, err := cache.ReadEvents(ctx, eventschema.EventTypePrompt, from); err != nil {
		t.Fatal(err)
	}
	// Take the oldest event away behind the cache's back (no trigger logs
	// it): an answer that still holds it came from memory.
	for _, q := range []string{`DROP TRIGGER events_changes_on_delete`, `DELETE FROM events WHERE id = 'oldest'`} {
		if _, err := store.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// A day later the same scorecard reaches a day less far back.
	got, err := cache.ReadEvents(ctx, eventschema.EventTypePrompt, from.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("a scorecard's read held %d of 2 events: it went to the store", len(got))
	}
}

// The scorecard route is handed the daemon's cache, when there is one.
func TestStartupHandsTheScorecardTheEventCache(t *testing.T) {
	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cache := newGlanceEvents(store)
	if got := (&startup{events: &eventRuntime{GlanceEvents: cache}}).glanceEvents(); got != cache {
		t.Errorf("handed %p, want the daemon's cache %p", got, cache)
	}
	for _, s := range []*startup{{}, {events: &eventRuntime{}}} {
		if got := s.glanceEvents(); got != nil {
			t.Errorf("handed %p without a cache, want none", got)
		}
	}
}
