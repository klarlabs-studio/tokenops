// Package events hosts the asynchronous event bus the proxy uses to ship
// observations (PromptEvent, WorkflowEvent, ...) to durable storage. The
// bus is intentionally simple: a buffered channel feeds a worker goroutine
// that batches envelopes into the sqlite store. When the channel fills,
// Publish drops the envelope and increments DroppedCount so the proxy hot
// path never blocks on storage backpressure.
package events

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Sink is the minimal contract the bus needs from a backing store. The
// sqlite store satisfies this; tests substitute fakes.
type Sink interface {
	AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error
}

// Bus accepts envelopes from emitters and ships them to a Sink. Publish
// is non-blocking; dropped envelopes are tracked but not retried.
type Bus interface {
	Publish(env *eventschema.Envelope)
	DroppedCount() int64
	PublishedCount() int64
	// Close stops the worker, draining queued envelopes within timeout.
	Close(timeout time.Duration) error
}

// AsyncBus is the production Bus implementation. Construct with NewAsync.
type AsyncBus struct {
	sink      Sink
	logger    *slog.Logger
	queue     chan *eventschema.Envelope
	batchSize int
	batchWait time.Duration

	dropped   atomic.Int64
	published atomic.Int64
	closed    atomic.Bool
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
}

// Options tunes AsyncBus. Zero values produce sensible defaults: 1024
// queue capacity, 64-envelope flush batches, 100ms batch wait.
type Options struct {
	QueueCapacity int
	BatchSize     int
	BatchWait     time.Duration
	Logger        *slog.Logger
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
	b := &AsyncBus{
		sink:      sink,
		logger:    opts.Logger,
		queue:     make(chan *eventschema.Envelope, opts.QueueCapacity),
		batchSize: opts.BatchSize,
		batchWait: opts.BatchWait,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
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
	default:
		b.dropped.Add(1)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := b.sink.AppendBatch(ctx, batch); err != nil {
			b.logger.Error("events: append batch", "size", len(batch), "err", err)
		}
		cancel()
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
