package events

import (
	"context"
	"testing"
	"time"
)

// A poller re-reads its whole source on every daemon start. Envelopes the
// store already holds must not reach the bus: each one costs a write
// transaction on a store other processes are waiting to use.
func TestSkipKnownDropsStoredEnvelopes(t *testing.T) {
	sink := &fakeSink{}
	inner := NewAsync(sink, Options{BatchSize: 1, BatchWait: 10 * time.Millisecond, Logger: discardLogger()})
	defer func() { _ = inner.Close(time.Second) }()
	bus := SkipKnown(inner, func(id string) bool { return id == "stored" })

	bus.Publish(newEnv("stored"))
	if err := bus.PublishWait(context.Background(), newEnv("stored")); err != nil {
		t.Fatalf("PublishWait(stored) = %v, want nil: a stored envelope is already persisted", err)
	}
	bus.Publish(newEnv("new-1"))
	if err := bus.PublishWait(context.Background(), newEnv("new-2")); err != nil {
		t.Fatalf("PublishWait(new) = %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sink.total() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.total(); got != 2 {
		t.Errorf("rows reaching the sink = %d, want 2 (only the new envelopes)", got)
	}
	if got := inner.PublishedCount(); got != 2 {
		t.Errorf("PublishedCount = %d, want 2", got)
	}
}

// Without a known set the wrapper must be the bus itself.
func TestSkipKnownNilIsPassThrough(t *testing.T) {
	inner := NewAsync(&fakeSink{}, Options{Logger: discardLogger()})
	defer func() { _ = inner.Close(time.Second) }()
	if got := SkipKnown(inner, nil); got != Bus(inner) {
		t.Fatalf("SkipKnown(bus, nil) = %T, want the bus unchanged", got)
	}
}
