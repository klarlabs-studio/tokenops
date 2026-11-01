package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/checkup"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type noEvents struct{}

func (noEvents) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return nil, nil
}

// The runtime keeps the overlay once after its delay, under the home it
// is given, and stops with its context.
func TestRunObservedKeepsTheOverlay(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, home)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	options := func(now time.Time) checkup.Options {
		return checkup.Options{Now: now, Events: noEvents{}, Plans: map[string]string{"anthropic": "claude-max"},
			Price: func(*eventschema.PromptEvent, time.Time) (float64, error) { return 1, nil }}
	}
	go func() {
		runObserved(ctx, options, 0, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	path := filepath.Join(home, ".tokenops", "observed-limits.json")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the overlay was not kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime did not stop with its context")
	}
}
