package domainevents

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBusConcurrentSubscribePublishUnsubscribe exercises subscribe /
// publish / cancel concurrently to surface races under `go test -race`.
// The bus must remain consistent: every published event is delivered to
// at-least the wildcard counter, and Cancel races never produce stale
// dispatches.
func TestBusConcurrentSubscribePublishUnsubscribe(t *testing.T) {
	var b Bus
	var wildcardHits atomic.Int64
	b.Subscribe("*", func(Event) { wildcardHits.Add(1) })

	const (
		publishers  = 8
		subscribers = 4
		iterations  = 200
	)
	var wg sync.WaitGroup
	wg.Add(publishers + subscribers)

	// Subscribers: register + cancel in a tight loop.
	for range subscribers {
		go func() {
			defer wg.Done()
			for range iterations {
				sub := b.Subscribe("workflow.started", func(Event) {})
				sub.Cancel()
			}
		}()
	}

	// Publishers: spray events of multiple kinds.
	for range publishers {
		go func() {
			defer wg.Done()
			for range iterations {
				b.Publish(WorkflowStarted{WorkflowID: "x", At: time.Now()})
				b.Publish(OptimizationApplied{OptimizerKind: "prompt_compress", At: time.Now()})
			}
		}()
	}
	wg.Wait()

	expectedMin := int64(publishers * iterations * 2)
	if wildcardHits.Load() < expectedMin {
		t.Errorf("wildcardHits = %d, want >= %d", wildcardHits.Load(), expectedMin)
	}
	if b.PanicCount() != 0 {
		t.Errorf("unexpected panic during stress: %d", b.PanicCount())
	}
}

// TestBusAsyncConcurrentPublish exercises async mode with many
// publishers feeding a single worker. The worker must drain every
// queued event; the only "loss" is via DroppedCount on overflow.
func TestBusAsyncConcurrentPublish(t *testing.T) {
	b := &Bus{}
	b.StartAsync(4096)
	defer b.Close()

	var hits atomic.Int64
	b.Subscribe("*", func(Event) { hits.Add(1) })

	const (
		publishers = 16
		iterations = 500
	)
	var wg sync.WaitGroup
	wg.Add(publishers)
	for range publishers {
		go func() {
			defer wg.Done()
			for range iterations {
				b.Publish(WorkflowStarted{WorkflowID: "x", At: time.Now()})
			}
		}()
	}
	wg.Wait()

	// Close drains the queue.
	b.Close()
	total := hits.Load() + b.DroppedCount()
	expected := int64(publishers * iterations)
	if total != expected {
		t.Errorf("dispatched(%d) + dropped(%d) = %d, want %d",
			hits.Load(), b.DroppedCount(), total, expected)
	}
}
