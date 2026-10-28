package config

import (
	"context"
	"strings"
	"testing"
	"time"
)

// failingSeer counts the window but cannot say when each source last
// produced an event, as a store that missed its deadline does.
type failingSeer struct{}

func (failingSeer) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (failingSeer) LastEventBySource(context.Context) (map[string]time.Time, error) {
	return nil, context.DeadlineExceeded
}

// A last-seen lookup that failed is not a source that was never ingested.
// The warning still stands, worded on the window it checked.
func TestCheckStaleIngestionDoesNotSayNeverWhenTheLookupFailed(t *testing.T) {
	cfg := Config{}
	cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true

	stale, err := cfg.CheckStaleIngestion(context.Background(), failingSeer{}, nil, StaleIngestionWindow, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 {
		t.Fatalf("got %d stale sources, want 1", len(stale))
	}
	if !stale[0].LastSeenUnknown {
		t.Error("the entry does not say its last event is unknown")
	}
	w := stale[0].Warning()
	if strings.Contains(w, "never") {
		t.Errorf("warning %q claims the source was never ingested", w)
	}
	if !strings.Contains(w, "in the last 48h") {
		t.Errorf("warning %q does not name the window", w)
	}
}
