package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// The daemon's aggregator reads session usage, and the waste report its
// recorded events and workflows, through the incremental readers unless
// storage.incremental_reads is off.
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
			ctx, cancel := context.WithCancel(context.Background())
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
			// The glance warm-up reads the store in the background: done
			// before the store's directory goes.
			t.Cleanup(func() { cancel(); _ = sup.Wait(10 * time.Second) })
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
			d := avoidable.DepsFor(cfg, components.Aggregator, components.Store, components.Spend)
			rt.wasteReaders(&d)
			_, recorded := d.Events.(*sqlite.RecordedEvents)
			if recorded != tc.cached || (rt.Traces != nil) != tc.cached {
				t.Fatalf("waste report reads through %T, traces %v; want incremental %v", d.Events, rt.Traces, tc.cached)
			}
			if d.Trace == nil {
				t.Fatal("the waste report lost its trace reader")
			}
			none := avoidable.Deps{}
			rt.wasteReaders(&none)
			if none.Events != nil || none.Trace != nil {
				t.Fatal("readers were given to a report that reads none")
			}
		})
	}
}
