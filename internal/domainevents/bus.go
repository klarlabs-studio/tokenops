// Package domainevents is the in-process domain event bus. It is
// distinct from internal/events (the telemetry envelope bus): this bus
// publishes typed *domain* events (WorkflowStarted, RuleCorpusReloaded,
// OptimizationApplied, ...) that subsystems consume to coordinate
// without storage round-trips.
//
// Sync mode (zero value): handlers run in the publisher's goroutine.
// Async mode (StartAsync): a single worker drains a buffered queue;
// overflows drop instead of blocking.
package domainevents

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Event is the marker interface every domain event implements. The
// Kind() method returns a stable string identifier used by subscribers
// to filter event streams.
type Event interface {
	Kind() string
}

// Handler is invoked once per published event whose kind matches the
// subscription. Handlers must be safe for concurrent use because
// publishers may run on different goroutines.
type Handler func(Event)

// Subscription is a handle returned by Subscribe. Cancel removes the
// handler from the bus. Idempotent.
type Subscription struct {
	bus  *Bus
	kind string
	id   uint64
	done atomic.Bool
}

// Cancel removes the subscription's handler from the bus. Idempotent.
func (s *Subscription) Cancel() {
	if s == nil || !s.done.CompareAndSwap(false, true) {
		return
	}
	s.bus.unsubscribe(s.kind, s.id)
}

type registered struct {
	id uint64
	fn Handler
}

// Bus is a small in-process pub/sub.
type Bus struct {
	mu             sync.RWMutex
	subs           map[string][]registered
	nextID         atomic.Uint64
	dispatched     atomic.Int64
	dropped        atomic.Int64
	slowDispatches atomic.Int64
	panics         atomic.Int64

	// SlowHandlerThreshold, when > 0, flags handlers that take longer
	// than this duration. Defaults to 0 (disabled).
	SlowHandlerThreshold time.Duration

	// PanicLogger receives a structured log entry for every handler
	// panic recovered by the bus. nil falls back to slog.Default().
	PanicLogger *slog.Logger

	queue       chan Event
	asyncOn     atomic.Bool
	queueClosed atomic.Bool
	workerWG    sync.WaitGroup
}

// StartAsync switches the bus to async dispatch with a buffered queue.
// Publish becomes non-blocking; the single worker drains the queue
// FIFO. queueCap clamped to 1 minimum. Calling StartAsync twice is a
// no-op.
func (b *Bus) StartAsync(queueCap int) {
	if !b.asyncOn.CompareAndSwap(false, true) {
		return
	}
	if queueCap < 1 {
		queueCap = 1
	}
	b.queue = make(chan Event, queueCap)
	b.workerWG.Add(1)
	go func() {
		defer b.workerWG.Done()
		for ev := range b.queue {
			b.dispatch(ev)
		}
	}()
}

// Close stops the async worker (if running) and drains the queue.
// Safe to call on a sync-mode bus. Concurrent Publish calls during
// Close fall back to sync dispatch via the closed flag.
func (b *Bus) Close() {
	if !b.asyncOn.CompareAndSwap(true, false) {
		return
	}
	b.queueClosed.Store(true)
	close(b.queue)
	b.workerWG.Wait()
}

// CloseWithTimeout is like Close but bounds drain duration. Returns
// false when the worker did not finish within d.
func (b *Bus) CloseWithTimeout(d time.Duration) bool {
	if !b.asyncOn.CompareAndSwap(true, false) {
		return true
	}
	b.queueClosed.Store(true)
	close(b.queue)
	done := make(chan struct{})
	go func() {
		b.workerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// Subscribe registers fn for every event whose Kind() matches kind.
// Pass "*" to receive every event. The returned Subscription removes
// the handler when Cancel is called. Re-registering the same function
// twice is allowed and fires twice — the bus does not dedupe.
func (b *Bus) Subscribe(kind string, fn Handler) *Subscription {
	if fn == nil {
		return nil
	}
	id := b.nextID.Add(1)
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[string][]registered{}
	}
	b.subs[kind] = append(b.subs[kind], registered{id: id, fn: fn})
	b.mu.Unlock()
	return &Subscription{bus: b, kind: kind, id: id}
}

func (b *Bus) unsubscribe(kind string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subs[kind]
	for i, s := range subs {
		if s.id == id {
			b.subs[kind] = append(subs[:i], subs[i+1:]...)
			return
		}
	}
}

// Publish dispatches ev. Sync mode runs handlers in the publisher's
// goroutine; async mode enqueues. Nil events drop.
func (b *Bus) Publish(ev Event) {
	if ev == nil {
		b.dropped.Add(1)
		return
	}
	if b.asyncOn.Load() {
		// queueClosed acts as a guard against the close→send race
		// (Close swaps asyncOn=false then closes the channel; a
		// concurrent Publish that already passed the asyncOn check
		// re-validates here and falls through to sync dispatch).
		if !b.queueClosed.Load() {
			select {
			case b.queue <- ev:
				return
			default:
				b.dropped.Add(1)
				return
			}
		}
	}
	b.dispatch(ev)
}

func (b *Bus) dispatch(ev Event) {
	b.mu.RLock()
	exact := append([]registered{}, b.subs[ev.Kind()]...)
	wild := append([]registered{}, b.subs["*"]...)
	threshold := b.SlowHandlerThreshold
	logger := b.PanicLogger
	b.mu.RUnlock()
	for _, h := range append(exact, wild...) {
		b.invoke(ev, h.fn, threshold, logger)
	}
	b.dispatched.Add(1)
}

// invoke runs h with panic recovery + slow-handler timing. A panicking
// handler increments PanicCount but does not kill the worker goroutine.
func (b *Bus) invoke(ev Event, h Handler, threshold time.Duration, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			b.panics.Add(1)
			if logger == nil {
				logger = slog.Default()
			}
			logger.Error("domainevents: handler panic", "kind", ev.Kind(), "panic", r)
		}
	}()
	if threshold > 0 {
		start := time.Now()
		h(ev)
		if time.Since(start) > threshold {
			b.slowDispatches.Add(1)
		}
		return
	}
	h(ev)
}

// DispatchedCount returns the number of events successfully published.
func (b *Bus) DispatchedCount() int64 { return b.dispatched.Load() }

// DroppedCount returns the number of nil or otherwise rejected events.
func (b *Bus) DroppedCount() int64 { return b.dropped.Load() }

// SlowDispatchCount returns the number of handler invocations that
// exceeded SlowHandlerThreshold. Always 0 when threshold is unset.
func (b *Bus) SlowDispatchCount() int64 { return b.slowDispatches.Load() }

// PanicCount returns the number of handler panics the bus has
// recovered from since process start.
func (b *Bus) PanicCount() int64 { return b.panics.Load() }
