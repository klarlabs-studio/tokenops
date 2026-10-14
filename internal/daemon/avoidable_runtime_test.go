package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
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
		runAvoidable(ctx, d, dir, time.Millisecond, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
