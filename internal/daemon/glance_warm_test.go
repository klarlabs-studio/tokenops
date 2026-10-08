package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// At start the daemon reads the glance's events into its cache in the
// background, from as far back as a glance reads: the first glance then
// answers from memory.
func TestDaemonWarmsTheGlanceCache(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, id := range []string{"warm-1", "warm-2"} {
		if err := store.Append(ctx, &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: headroom.EventsFloor(now).Add(time.Minute), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-5", InputTokens: 10},
		}); err != nil {
			t.Fatal(err)
		}
	}

	glance := newGlanceEvents(store)
	sup := lifecycle.New(ctx, logger)
	warmGlanceEvents(sup, glance, logger)
	if err := sup.Wait(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if failed := sup.Failed(); len(failed) != 0 {
		t.Fatalf("warm-up failed: %v", failed)
	}

	// Take the event away behind the cache's back (no trigger logs it):
	// an answer that still holds it came from the warmed memory.
	for _, q := range []string{`DROP TRIGGER events_changes_on_delete`, `DELETE FROM events WHERE id = 'warm-1'`} {
		if _, err := store.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// The glance comes after the warm-up, and reads from its own now.
	got, err := glance.ReadEvents(ctx, eventschema.EventTypePrompt, headroom.EventsFloor(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "warm-1" {
		t.Fatalf("the first glance read the store, not the warmed cache: %d events", len(got))
	}
}
