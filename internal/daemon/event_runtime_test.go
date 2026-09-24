package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestInitializeEventRuntimePersistsCanonicalEnvelope(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)
	components, err := bootstrap.New(ctx, bootstrap.Options{Logger: logger, OpenStore: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = components.Shutdown() })

	cfg := config.Config{Storage: config.StorageConfig{
		Enabled: true,
		Path:    filepath.Join(t.TempDir(), "events.db"),
	}}
	rt, err := initializeEventRuntime(ctx, cfg, components, components.EventCounter, "", freshness.NewRegistry(), lifecycle.New(ctx, logger), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Detach()

	env, err := eventschema.NewDomainEnvelope("test.runtime", map[string]string{"ok": "yes"}, time.Now(), "event-runtime-test", eventschema.Association{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Bus.PublishWait(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := rt.Drain(time.Second); err != nil {
		t.Fatal(err)
	}
	rows, err := rt.Store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeDomain, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("persisted domain envelopes = %d, want 1", len(rows))
	}
}

func TestInitializeEventRuntimeWithoutStorageUsesNoopBus(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)
	components, err := bootstrap.New(ctx, bootstrap.Options{Logger: logger, OpenStore: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = components.Shutdown() })
	rt, err := initializeEventRuntime(ctx, config.Config{}, components, observ.NewEventCounter(), "", freshness.NewRegistry(), lifecycle.New(ctx, logger), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Detach()
	if rt.Store != nil || rt.Bus == nil {
		t.Fatalf("runtime store=%v bus=%v, want nil store and non-nil bus", rt.Store, rt.Bus)
	}
	if err := rt.Drain(time.Second); err != nil {
		t.Fatal(err)
	}
}
