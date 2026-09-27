package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestLoadStoredIDsKnowsExactlyTheStoredEnvelopes(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()
	for _, id := range []string{"ccj-00112233445566", "cdx-8899aabbccddeeff"} {
		env := &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: time.Now(), Source: "test",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	known, n, err := loadStoredIDs(ctx, store)
	if err != nil {
		t.Fatalf("loadStoredIDs: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
	for id, want := range map[string]bool{
		"ccj-00112233445566":   true,
		"cdx-8899aabbccddeeff": true,
		"ccj-ffffffffffffffff": false,
		"":                     false,
	} {
		if got := known(id); got != want {
			t.Errorf("known(%q) = %v, want %v", id, got, want)
		}
	}
}
