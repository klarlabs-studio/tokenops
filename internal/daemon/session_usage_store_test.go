package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// The daemon's aggregator reads session usage through the incremental
// cache unless storage.incremental_reads is off.
func TestEventRuntimeFollowsIncrementalReads(t *testing.T) {
	off, on := false, true
	for _, tc := range []struct {
		name   string
		set    *bool
		cached bool
	}{
		{"default", nil, true},
		{"on", &on, true},
		{"off", &off, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			logger := slog.New(slog.DiscardHandler)
			components, err := bootstrap.New(ctx, bootstrap.Options{Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = components.Shutdown() })
			cfg := config.Config{Storage: config.StorageConfig{
				Enabled: true, Path: filepath.Join(t.TempDir(), "events.db"), IncrementalReads: tc.set,
			}}
			sup := lifecycle.New(ctx, logger)
			rt, err := initializeEventRuntime(ctx, cfg, components, components.EventCounter, "", freshness.NewRegistry(), sup, logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(rt.Detach)
			if cached := components.SessionUsage != nil; cached != tc.cached {
				t.Fatalf("session usage cached = %v, want %v", cached, tc.cached)
			}
			if components.Aggregator == nil {
				t.Fatal("no aggregator")
			}
		})
	}
}
