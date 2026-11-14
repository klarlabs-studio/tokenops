package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A derived figure's inputs move with every event added, updated or
// deleted, with a file it reads, and with the rate card in force; and
// stand still otherwise, a window reading stored again unchanged
// included.
func TestDerivedInputsMoveWithWhatTheFiguresRead(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	file := filepath.Join(t.TempDir(), "observed-limits.json")
	inputs := derivedInputs(store, "cfg", file)
	add := func(id string) {
		if err := store.Append(ctx, &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: time.Now().UTC(), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-5", InputTokens: 10},
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("first")
	steps := []struct {
		name  string
		do    func()
		moves bool
	}{
		{name: "nothing", do: func() {}},
		{name: "an event added", do: func() { add("second") }, moves: true},
		{name: "an event deleted", do: func() {
			if _, err := store.DB().ExecContext(ctx, `DELETE FROM events WHERE id = 'first'`); err != nil {
				t.Fatal(err)
			}
		}, moves: true},
		{name: "a file it reads written", do: func() {
			if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, moves: true},
		{name: "a rate card applied", do: func() { pricingGeneration.Add(1) }, moves: true},
		{name: "a window reading stored again unchanged", do: func() {
			if err := store.Append(ctx, &eventschema.Envelope{
				ID: "repeat", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
				Timestamp: time.Now().UTC(), Source: "codex-app-server",
				Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, Status: 200},
				Attributes: map[string]string{"granularity": "quota_snapshot", "primary_used_pct": "40.00",
					eventschema.AttrRepeat: eventschema.AttrRepeatValue},
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "nothing again", do: func() {}},
	}
	last, ok := inputs(ctx)
	if !ok {
		t.Fatal("inputs unknown on a readable store")
	}
	for _, st := range steps {
		st.do()
		got, ok := inputs(ctx)
		if !ok {
			t.Fatalf("%s: inputs unknown", st.name)
		}
		if moved := got != last; moved != st.moves {
			t.Errorf("%s: moved %v, want %v (%q -> %q)", st.name, moved, st.moves, last, got)
		}
		last = got
	}
	if other, _ := derivedInputs(store, "other config", file)(ctx); other == last {
		t.Error("another config's inputs read the same")
	}
	if _, ok := derivedInputs(nil, "cfg")(ctx); ok {
		t.Error("inputs known without a store")
	}
}
