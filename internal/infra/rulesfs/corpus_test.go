package rulesfs

import (
	"testing"
	"testing/fstest"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type envelopeCapture struct{ events []*eventschema.Envelope }

func (c *envelopeCapture) Publish(env *eventschema.Envelope) { c.events = append(c.events, env) }

func TestLoadCorpusFromFSDeduplicatesReloadEvents(t *testing.T) {
	memFS := fstest.MapFS{
		"CLAUDE.md": {Data: []byte("# Testing\nuse tdd\n")},
	}
	bus := &envelopeCapture{}
	SetEventBus(bus)
	t.Cleanup(func() { SetEventBus(nil) })

	if _, err := LoadCorpusFromFS(memFS, "repo"); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if _, err := LoadCorpusFromFS(memFS, "repo"); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(bus.events) != 1 {
		t.Errorf("reloads = %d, want 1 (corpus unchanged)", len(bus.events))
	}

	// Mutate corpus → must fire again.
	memFS["CLAUDE.md"] = &fstest.MapFile{Data: []byte("# Testing\nuse tdd everywhere\n")}
	if _, err := LoadCorpusFromFS(memFS, "repo"); err != nil {
		t.Fatalf("third load: %v", err)
	}
	if len(bus.events) != 2 {
		t.Errorf("reloads after change = %d, want 2", len(bus.events))
	}
}
