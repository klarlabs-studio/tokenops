package rulesfs

import (
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
)

func TestLoadCorpusFromFSDeduplicatesReloadEvents(t *testing.T) {
	memFS := fstest.MapFS{
		"CLAUDE.md": {Data: []byte("# Testing\nuse tdd\n")},
	}
	bus := &domainevents.Bus{}
	var reloads atomic.Int64
	bus.Subscribe(domainevents.KindRuleCorpusReloaded, func(domainevents.Event) { reloads.Add(1) })
	SetDomainBus(bus)
	t.Cleanup(func() { SetDomainBus(nil) })

	if _, err := LoadCorpusFromFS(memFS, "repo"); err != nil {
		t.Fatalf("first load: %v", err)
	}
	if _, err := LoadCorpusFromFS(memFS, "repo"); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if reloads.Load() != 1 {
		t.Errorf("reloads = %d, want 1 (corpus unchanged)", reloads.Load())
	}

	// Mutate corpus → must fire again.
	memFS["CLAUDE.md"] = &fstest.MapFile{Data: []byte("# Testing\nuse tdd everywhere\n")}
	if _, err := LoadCorpusFromFS(memFS, "repo"); err != nil {
		t.Fatalf("third load: %v", err)
	}
	if reloads.Load() != 2 {
		t.Errorf("reloads after change = %d, want 2", reloads.Load())
	}
}
