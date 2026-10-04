package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// A daemon stopping before the first analysis returns at once instead of
// waiting the analysis out.
func TestSessionFindingsStopsBeforeTheFirstRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		runSessionFindings(ctx, t.TempDir(), time.Hour, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop")
	}
}
