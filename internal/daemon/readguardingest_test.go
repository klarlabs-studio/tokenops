package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// recordingBus counts which publish path each envelope took.
type recordingBus struct {
	mu       sync.Mutex
	dropping int // Publish: silently discards on a full queue
	waiting  int // PublishWait: blocks for room
}

func (b *recordingBus) Publish(*eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dropping++
}

func (b *recordingBus) PublishWait(_ context.Context, _ *eventschema.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.waiting++
	return nil
}
func (b *recordingBus) PublishedCount() int64 { return 0 }
func (b *recordingBus) DroppedCount() int64   { return 0 }
func (b *recordingBus) Close(time.Duration) error {
	return nil
}

func seedLedger(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for i := range n {
		line := fmt.Sprintf(
			`{"ts":"2026-09-18T10:%02d:00Z","mode":"active","session":"s","path":"/a%d.go","action":"blocked","est_tokens":%d}`,
			i%60, i, 100+i)
		if _, err := fmt.Fprintln(f, line); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Every vendor-usage poller publishes through PublishWait. This ingest did
// not, and it is the burst most likely to overflow: it replays the WHOLE
// ledger at boot and again every two minutes.
//
// On one machine that dropped 222 events in the 13 seconds after start —
// the entire read-guard history — which only became visible when the drop
// counter was finally surfaced. Re-scanning makes the loss recoverable, but
// a counter that is non-zero on every boot is one an operator learns to
// ignore, which is the failure the counter exists to prevent.
func TestReadGuardIngestWaitsRatherThanDrops(t *testing.T) {
	dir := seedLedger(t, 40)
	bus := &recordingBus{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // one scan, then return

	runReadGuardIngest(ctx, bus, slog.New(slog.DiscardHandler), time.Minute, dir)

	if bus.dropping != 0 {
		t.Errorf("%d envelopes took the dropping path; ingestion must not use Publish", bus.dropping)
	}
	if bus.waiting != 40 {
		t.Errorf("PublishWait calls = %d, want 40", bus.waiting)
	}
}

// A cancelled context must stop the scan rather than spin through the rest
// of the ledger against a bus that can no longer accept anything.
func TestReadGuardIngestStopsWhenCancelled(t *testing.T) {
	dir := seedLedger(t, 5)
	bus := &recordingBus{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runReadGuardIngest(ctx, bus, slog.New(slog.DiscardHandler), time.Minute, dir)
	if bus.dropping != 0 {
		t.Errorf("used the dropping path %d times", bus.dropping)
	}
}
