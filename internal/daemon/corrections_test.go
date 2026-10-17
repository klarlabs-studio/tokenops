package daemon

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// TestStartCorrectionsDoesNotHoldTheBoot pins why a restart answers
// quickly: the start-up corrections rewrite recorded history, which
// nothing that serves needs first, so the boot hands them to the
// supervisor and goes on to open the port while they run.
func TestStartCorrectionsDoesNotHoldTheBoot(t *testing.T) {
	release, ran := make(chan struct{}), make(chan struct{})
	prev := runStartupCorrections
	runStartupCorrections = func(ctx context.Context, _ config.Config, _ *sqlite.Store, _ *routehistory.Tracker, _ *slog.Logger) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		close(ran)
	}
	t.Cleanup(func() { runStartupCorrections = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, _ := newTestStartup(validConfig())
	s.sup = lifecycle.New(ctx, s.logger)

	done := make(chan struct{})
	go func() { s.startCorrections(&sqlite.Store{}, nil); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("startCorrections waited for the corrections")
	}
	close(release)
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the corrections never ran")
	}
	if err := s.sup.Wait(time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestStartCorrectionsSkipsWithoutAStore keeps a storage-less daemon
// from starting a task with nothing to correct.
func TestStartCorrectionsSkipsWithoutAStore(t *testing.T) {
	called := false
	prev := runStartupCorrections
	runStartupCorrections = func(context.Context, config.Config, *sqlite.Store, *routehistory.Tracker, *slog.Logger) {
		called = true
	}
	t.Cleanup(func() { runStartupCorrections = prev })
	s, _ := newTestStartup(validConfig())
	s.sup = lifecycle.New(context.Background(), s.logger)
	s.startCorrections(nil, nil)
	if err := s.sup.Wait(time.Second); err != nil || called {
		t.Fatalf("called %v, err %v", called, err)
	}
}
