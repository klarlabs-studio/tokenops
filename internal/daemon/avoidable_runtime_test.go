package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/effortweeks"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// The week's waste report is stored after the delay, and the loop stops
// with the daemon.
func TestAvoidableRuntimeStoresASnapshot(t *testing.T) {
	dir := t.TempDir()
	d := avoidable.Deps{
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return nil, nil },
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runAvoidable(ctx, d, dir, time.Millisecond, time.Hour, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for {
		if s, err := avoidable.ReadSnapshot(dir); err == nil && s != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no snapshot stored")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop")
	}
}

// Once the week's report is stored, the compaction rollup follows the
// event log: the last four finished weeks, computed from the same readers.
// HOME and XDG_CONFIG_HOME are isolated: the step reads the agents'
// settings and the coach's ledgers there.
func TestAfterAvoidableStoresTheCompactionWeeks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(home, "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := avoidable.Deps{
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return nil, nil },
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return nil },
	}
	dir := filepath.Join(home, "cache")
	afterAvoidable(ctx, config.Config{}, d, store, dir, avoidable.Snapshot{}, time.Now(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	w, err := avoidable.ReadWeeks(dir)
	if err != nil || w == nil {
		t.Fatalf("no rollup stored: %v", err)
	}
	if len(w.Weeks) != 4 || w.Version != avoidable.WeeksVersion {
		t.Errorf("rollup = %d weeks, version %d; want the last four finished weeks", len(w.Weeks), w.Version)
	}
}

// The effort rollup is stored beside it, from the transcripts and the
// turns' prices, for the last Kept finished weeks (ADR 0022).
func TestAfterAvoidableStoresTheEffortWeeks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(home, "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := avoidable.Deps{
		Turns:   func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return nil, nil },
		Dirs:    func(time.Time) map[string]sessiondirs.Dir { return nil },
		Records: func(context.Context, time.Time, time.Time) ([]agentdx.Record, error) { return nil, nil },
	}
	dir := filepath.Join(home, "cache")
	afterAvoidable(ctx, config.Config{}, d, store, dir, avoidable.Snapshot{}, time.Now(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	r, err := effortweeks.Read(dir)
	if err != nil || r == nil {
		t.Fatalf("no effort rollup stored: %v", err)
	}
	if len(r.Weeks) != effortweeks.Kept || r.Version != effortweeks.Version {
		t.Errorf("rollup = %d weeks, version %d", len(r.Weeks), r.Version)
	}
}
