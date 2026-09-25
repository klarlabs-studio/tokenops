package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func TestRetentionRuntimeStopsUnderSupervisor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	sup := lifecycle.New(ctx, slog.New(slog.DiscardHandler))
	cfg := config.RetentionConfig{Keep: map[string]string{"prompt": "1h"}}
	if err := startRetentionRuntime(cfg, store, sup, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := sup.Wait(time.Second); err != nil {
		t.Fatalf("supervisor wait: %v", err)
	}
	if got := sup.Running(); len(got) != 0 {
		t.Fatalf("retention task still running: %v", got)
	}
}
