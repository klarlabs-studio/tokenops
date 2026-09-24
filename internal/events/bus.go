// Package events hosts the asynchronous event bus the proxy uses to ship
// observations (PromptEvent, WorkflowEvent, ...) to durable storage. The
// bus is intentionally simple: a buffered channel feeds a worker goroutine
// that batches envelopes into the sqlite store. When the channel fills,
// Publish drops the envelope and increments DroppedCount so the proxy hot
// path never blocks on storage backpressure.
//
// That trade is right for the proxy and wrong for everything else. A
// backfill poller has no latency budget to protect: dropping its envelopes
// does not keep a request fast, it just loses history that nothing will
// ever re-read, because the poller has already marked the row as seen.
// Ingestion therefore uses PublishWait, which waits for room instead.
package events

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Retry defaults. A rejected batch is almost always a slow store rather
// than a broken one — a large WAL, a checkpoint in flight, another writer
// holding the lock — so a few spaced attempts recover it where discarding
// it loses every row for good.
const (
	defaultFlushRetries   = 4
	defaultFlushRetryWait = 250 * time.Millisecond
	// flushTimeout bounds one AppendBatch attempt.
	flushTimeout = 5 * time.Second
)

// Sink is the minimal contract the bus needs from a backing store. The
// sqlite store satisfies this; tests substitute fakes.
type Sink interface {
	AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error
}

// NoopSink discards accepted envelopes. It is used when the daemon runs
// without persistence but still needs canonical in-process event observers.
type NoopSink struct{}

// AppendBatch satisfies Sink and intentionally discards the batch.
func (NoopSink) AppendBatch(context.Context, []*eventschema.Envelope) error { return nil }

// ErrBusClosed reports that the bus stopped accepting envelopes.
var ErrBusClosed = errors.New("events: bus closed")

// Bus accepts envelopes from emitters and ships them to a Sink. Publish
// is non-blocking; dropped envelopes are tracked but not retried.
type Bus interface {
	Publish(env *eventschema.Envelope)
	// PublishWait enqueues env, blocking until there is room or ctx ends.
	// Backfill and ingestion paths use it so a slow sink throttles the
	// reader rather than silently shedding its rows.
	PublishWait(ctx context.Context, env *eventschema.Envelope) error
	DroppedCount() int64
	PublishedCount() int64
	// Close stops the worker, draining queued envelopes within timeout.
	Close(timeout time.Duration) error
}

// Observable is implemented by buses that can notify lightweight local
// consumers when an envelope is accepted for dispatch. Observers must not
// mutate the envelope and should return quickly; they run on the publisher.
type Observable interface {
	Subscribe(func(*eventschema.Envelope)) func()
}

// AsyncBus is the production Bus implementation. Construct with NewAsync.
type AsyncBus struct {
	sink      Sink
	logger    *slog.Logger
	queue     chan *eventschema.Envelope
	batchSize int
	batchWait time.Duration

	flushRetries   int
	flushRetryWait time.Duration

	dropped   atomic.Int64
	published atomic.Int64
	closed    atomic.Bool
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
	subsMu    sync.RWMutex
	subs      map[uint64]func(*eventschema.Envelope)
	nextSubID uint64
}

// Options tunes AsyncBus. Zero values produce sensible defaults: 1024
// queue capacity, 64-envelope flush batches, 100ms batch wait.
type Options struct {
	QueueCapacity int
	BatchSize     int
	BatchWait     time.Duration
	Logger        *slog.Logger
	// FlushRetries is how many extra attempts a rejected batch gets
	// before the bus gives up and counts its rows as dropped. Zero uses
	// the default; negative disables retrying.
	FlushRetries int
	// FlushRetryWait is the initial backoff between those attempts. It
	// doubles each time. Zero uses the default.
	FlushRetryWait time.Duration
}

// NewAsync constructs an AsyncBus and starts its worker goroutine.
func NewAsync(sink Sink, opts Options) *AsyncBus {
	if opts.QueueCapacity <= 0 {
		opts.QueueCapacity = 1024
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 64
	}
	if opts.BatchWait <= 0 {
		opts.BatchWait = 100 * time.Millisecond
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.FlushRetries == 0 {
		opts.FlushRetries = defaultFlushRetries
	}
	if opts.FlushRetries < 0 {
		opts.FlushRetries = 0
	}
	if opts.FlushRetryWait <= 0 {
		opts.FlushRetryWait = defaultFlushRetryWait
	}
	b := &AsyncBus{
		sink:           sink,
		logger:         opts.Logger,
		queue:          make(chan *eventschema.Envelope, opts.QueueCapacity),
		batchSize:      opts.BatchSize,
		batchWait:      opts.BatchWait,
		flushRetries:   opts.FlushRetries,
		flushRetryWait: opts.FlushRetryWait,
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
		subs:           make(map[uint64]func(*eventschema.Envelope)),
	}
	go b.run()
	return b
}

// Publish enqueues env. If the queue is full or the bus is closed, env is
// dropped and DroppedCount increments. nil envelopes are ignored.
func (b *AsyncBus) Publish(env *eventschema.Envelope) {
	if env == nil || b.closed.Load() {
		return
	}
	select {
	case b.queue <- env:
		b.published.Add(1)
		b.notify(env)
	default:
		b.dropped.Add(1)
	}
}

// PublishWait enqueues env, waiting for queue room rather than dropping.
// It returns ctx.Err() if ctx ends first, and ErrBusClosed once the bus is
// closed, so a caller can tell "not stored" from "stored".
//
// Ingestion uses this instead of Publish. A poller marks each row seen
// before publishing, so a dropped envelope is never retried on a later
// scan: the loss is permanent and invisible, and it reproduces on every
// restart because the queue saturates at the same point each pass.
func (b *AsyncBus) PublishWait(ctx context.Context, env *eventschema.Envelope) error {
	if env == nil {
		return nil
	}
	if b.closed.Load() {
		return ErrBusClosed
	}
	select {
	case b.queue <- env:
		b.published.Add(1)
		b.notify(env)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe registers a lightweight observer for accepted envelopes. The
// returned function cancels the subscription and is safe to call repeatedly.
func (b *AsyncBus) Subscribe(fn func(*eventschema.Envelope)) func() {
	if b == nil || fn == nil {
		return func() {}
	}
	b.subsMu.Lock()
	b.nextSubID++
	id := b.nextSubID
	b.subs[id] = fn
	b.subsMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.subsMu.Lock()
			delete(b.subs, id)
			b.subsMu.Unlock()
		})
	}
}

func (b *AsyncBus) notify(env *eventschema.Envelope) {
	b.subsMu.RLock()
	callbacks := make([]func(*eventschema.Envelope), 0, len(b.subs))
	for _, fn := range b.subs {
		callbacks = append(callbacks, fn)
	}
	b.subsMu.RUnlock()
	for _, fn := range callbacks {
		fn(env)
	}
}

// DroppedCount returns the number of envelopes dropped due to backpressure.
func (b *AsyncBus) DroppedCount() int64 { return b.dropped.Load() }

// PublishedCount returns the number of envelopes successfully enqueued
// (not necessarily yet flushed to the sink).
func (b *AsyncBus) PublishedCount() int64 { return b.published.Load() }

// Close stops the worker after draining within timeout.
func (b *AsyncBus) Close(timeout time.Duration) error {
	var firstErr error
	b.once.Do(func() {
		b.closed.Store(true)
		close(b.stop)
		select {
		case <-b.done:
		case <-time.After(timeout):
			firstErr = errors.New("events: drain timeout exceeded")
		}
	})
	return firstErr
}

func (b *AsyncBus) run() {
	defer close(b.done)
	batch := make([]*eventschema.Envelope, 0, b.batchSize)
	timer := time.NewTimer(b.batchWait)
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(b.batchWait)
	}

	flush := func() {
		if len(batch) == 0 {
			return
		}
		wait := b.flushRetryWait
		var err error
		for attempt := 0; attempt <= b.flushRetries; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
			err = b.sink.AppendBatch(ctx, batch)
			cancel()
			if err == nil {
				break
			}
			if attempt == b.flushRetries {
				break
			}
			b.logger.Warn("events: append batch failed, retrying",
				"size", len(batch), "attempt", attempt+1, "err", err)
			select {
			case <-time.After(wait):
			case <-b.stop:
				// Shutting down: spend the remaining attempts now
				// rather than sleeping through the drain deadline.
			}
			wait *= 2
		}
		if err != nil {
			// Every row in this batch is lost. Say so in the rows the
			// operator counts, not just as an opaque batch failure:
			// the old code discarded the batch and reported nothing,
			// which is how a third of one client's history went
			// missing without a single error an operator could see.
			b.dropped.Add(int64(len(batch)))
			b.logger.Error("events: append batch gave up, rows lost",
				"rows", len(batch), "attempts", b.flushRetries+1, "err", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case env, ok := <-b.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, env)
			if len(batch) >= b.batchSize {
				flush()
				resetTimer()
			}
		case <-timer.C:
			flush()
			resetTimer()
		case <-b.stop:
			// Drain any queued envelopes.
			drained := true
			for drained {
				select {
				case env := <-b.queue:
					batch = append(batch, env)
					if len(batch) >= b.batchSize {
						flush()
					}
				default:
					drained = false
				}
			}
			flush()
			return
		}
	}
}

// Noop is a Bus that discards everything. Useful as a default when event
// emission is disabled.
type Noop struct{}

// Publish discards env.
func (Noop) Publish(*eventschema.Envelope) {}

// DroppedCount always returns 0.
func (Noop) DroppedCount() int64 { return 0 }

// PublishedCount always returns 0.
func (Noop) PublishedCount() int64 { return 0 }

// Close is a no-op.
func (Noop) Close(time.Duration) error { return nil }
