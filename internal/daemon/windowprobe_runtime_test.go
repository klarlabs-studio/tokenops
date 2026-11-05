package daemon

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func TestWindowPressureRuntimeDoesNotRegisterWithoutStore(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := lifecycle.New(t.Context(), logger)
	rc := &router.Config{}
	if got := startWindowPressureRuntime(config.Default(), rc, nil, sup, logger); got != nil {
		t.Fatal("runtime returned a probe without an event store")
	}
	if rc.WindowPressure != nil {
		t.Fatal("runtime installed a window-pressure reader without an event store")
	}
	if got := sup.Running(); len(got) != 0 {
		t.Fatalf("runtime registered tasks without an event store: %v", got)
	}
}

// The probe reads through the glance's event cache when the daemon has
// one: read from the store, its two-week window was a full read of every
// event, decoded, once a minute.
func TestWindowPressureReadsThroughTheGlanceCache(t *testing.T) {
	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cache := newGlanceEvents(store)

	s := &startup{components: &bootstrap.Components{Store: store}, events: &eventRuntime{GlanceEvents: cache}}
	if got := s.pressureEvents(); got != headroom.EventReader(cache) {
		t.Fatalf("the probe reads %T, want the glance's cache", got)
	}
	s.events = &eventRuntime{}
	if got := s.pressureEvents(); got != headroom.EventReader(store) {
		t.Fatalf("without a cache the probe reads %T, want the store", got)
	}
	s.components = &bootstrap.Components{}
	if got := s.pressureEvents(); got != nil {
		t.Fatalf("without a store the probe reads %T, want nothing", got)
	}
}
