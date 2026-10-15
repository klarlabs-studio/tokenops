package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/anomalies"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// The anomaly run stores its snapshot after the delay, and the loop
// stops with the daemon.
func TestAnomalyRuntimeStoresASnapshot(t *testing.T) {
	dir := t.TempDir()
	d := anomalies.Deps{
		Turns:   func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return nil, nil },
		Charges: func(context.Context, time.Time, time.Time) ([]analytics.Charge, error) { return nil, nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runAnomalies(ctx, d, dir, time.Millisecond, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for {
		if s, err := anomalies.ReadSnapshot(dir); err == nil && s != nil {
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
