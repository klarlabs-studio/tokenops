package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

type fakeSink struct {
	mu      sync.Mutex
	batches int
	rows    []*eventschema.Envelope
	err     error
	calls   atomic.Int64
}

func (f *fakeSink) AppendBatch(_ context.Context, envs []*eventschema.Envelope) error {
	f.calls.Add(1)
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches++
	f.rows = append(f.rows, envs...)
	return nil
}

func (f *fakeSink) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func newEnv(id string) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID:            id,
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC(),
		Source:        "test",
		Payload: &eventschema.PromptEvent{
			PromptHash: "h", Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
		},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPublishFlushesOnBatchSize(t *testing.T) {
	sink := &fakeSink{}
	bus := NewAsync(sink, Options{BatchSize: 5, BatchWait: 50 * time.Millisecond, Logger: discardLogger()})
	for i := 0; i < 10; i++ {
		bus.Publish(newEnv("e" + string(rune('0'+i))))
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sink.total() < 10 {
		time.Sleep(10 * time.Millisecond)
	}
	if sink.total() != 10 {
		t.Errorf("sink total = %d, want 10", sink.total())
	}
	if err := bus.Close(time.Second); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestPublishDropsWhenQueueFull(t *testing.T) {
	// Block sink so the worker never drains. Queue fills, drops increment.
	block := make(chan struct{})
	sink := &slowSink{block: block}
	bus := NewAsync(sink, Options{
		QueueCapacity: 2, BatchSize: 1, BatchWait: time.Hour, Logger: discardLogger(),
	})
	defer func() { close(block); _ = bus.Close(time.Second) }()

	for i := 0; i < 50; i++ {
		bus.Publish(newEnv("e"))
	}
	// Allow worker to take one off the queue and block in sink.
	time.Sleep(50 * time.Millisecond)
	if got := bus.DroppedCount(); got == 0 {
		t.Errorf("expected drops, got 0 (published=%d)", bus.PublishedCount())
	}
}

type slowSink struct {
	block chan struct{}
}

func (s *slowSink) AppendBatch(_ context.Context, _ []*eventschema.Envelope) error {
	<-s.block
	return nil
}

func TestCloseDrainsQueue(t *testing.T) {
	sink := &fakeSink{}
	bus := NewAsync(sink, Options{
		BatchSize: 100, BatchWait: time.Hour, // never time-flushes
		Logger: discardLogger(),
	})
	for i := 0; i < 10; i++ {
		bus.Publish(newEnv("e"))
	}
	if err := bus.Close(2 * time.Second); err != nil {
		t.Fatalf("close: %v", err)
	}
	if sink.total() != 10 {
		t.Errorf("close did not drain: total=%d", sink.total())
	}
}

func TestCloseIdempotent(t *testing.T) {
	bus := NewAsync(&fakeSink{}, Options{Logger: discardLogger()})
	if err := bus.Close(time.Second); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := bus.Close(time.Second); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestPublishAfterCloseDropped(t *testing.T) {
	bus := NewAsync(&fakeSink{}, Options{Logger: discardLogger()})
	_ = bus.Close(time.Second)
	bus.Publish(newEnv("after"))
	if bus.PublishedCount() != 0 {
		t.Errorf("publish after close should drop, got %d", bus.PublishedCount())
	}
}

func TestSinkErrorLoggedNotFatal(t *testing.T) {
	sink := &fakeSink{err: errors.New("disk full")}
	bus := NewAsync(sink, Options{
		BatchSize: 2, BatchWait: 50 * time.Millisecond, Logger: discardLogger(),
	})
	defer bus.Close(time.Second)
	bus.Publish(newEnv("a"))
	bus.Publish(newEnv("b"))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sink.calls.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if sink.calls.Load() == 0 {
		t.Errorf("sink never called")
	}
	// Bus must still accept new publishes after an error.
	bus.Publish(newEnv("c"))
}

func TestNoopBus(t *testing.T) {
	b := Noop{}
	b.Publish(newEnv("x"))
	if b.PublishedCount() != 0 || b.DroppedCount() != 0 {
		t.Error("Noop should always be zero")
	}
	if err := b.Close(time.Second); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestPublishNilIgnored(t *testing.T) {
	bus := NewAsync(&fakeSink{}, Options{Logger: discardLogger()})
	defer bus.Close(time.Second)
	bus.Publish(nil)
	if bus.PublishedCount() != 0 {
		t.Errorf("nil should not increment published")
	}
}

func TestBatchWaitFlushes(t *testing.T) {
	sink := &fakeSink{}
	bus := NewAsync(sink, Options{
		BatchSize: 100, BatchWait: 80 * time.Millisecond, Logger: discardLogger(),
	})
	defer bus.Close(time.Second)
	bus.Publish(newEnv("single"))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sink.total() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if sink.total() != 1 {
		t.Errorf("BatchWait did not flush: total=%d", sink.total())
	}
}
