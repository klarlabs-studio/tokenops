package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
)

func atomicAdd(p *int64)        { atomic.AddInt64(p, 1) }
func atomicLoad(p *int64) int64 { return atomic.LoadInt64(p) }

// SubscribeOptions tunes the subscriber's backpressure behavior.
type SubscribeOptions struct {
	// Actor identifies the system principal recording entries. Empty
	// defaults to "daemon".
	Actor string
	// MaxConcurrent caps in-flight recorder goroutines. Excess events
	// are dropped (counted via DroppedCount on the returned Subscriber)
	// rather than spawning unbounded goroutines. Zero defaults to 16.
	MaxConcurrent int
}

// Subscriber is the long-lived audit handle wired into the domain bus.
// DroppedCount reflects events shed due to backpressure since the
// subscriber was created; useful for dashboards / health probes.
type Subscriber struct {
	rec    *Recorder
	logger *slog.Logger
	actor  string
	sem    chan struct{}
	drops  int64
	wg     sync.WaitGroup
	closed atomic.Bool
}

// Subscribe attaches the audit recorder to bus so the audit log captures
// security-relevant domain events (budget breaches, applied
// optimizations) without each publisher knowing about audit. The
// recorder runs in a bounded goroutine pool — excess events are dropped
// and counted instead of spawning unbounded goroutines.
func Subscribe(bus *domainevents.Bus, rec *Recorder, logger *slog.Logger, actor string) *Subscriber {
	return SubscribeWithOptions(bus, rec, logger, SubscribeOptions{Actor: actor})
}

// SubscribeWithOptions is the configurable form of Subscribe.
func SubscribeWithOptions(bus *domainevents.Bus, rec *Recorder, logger *slog.Logger, opts SubscribeOptions) *Subscriber {
	if bus == nil || rec == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if opts.Actor == "" {
		opts.Actor = "daemon"
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 16
	}
	sub := &Subscriber{
		rec:    rec,
		logger: logger,
		actor:  opts.Actor,
		sem:    make(chan struct{}, opts.MaxConcurrent),
	}
	bus.Subscribe(domainevents.KindBudgetExceeded, sub.handle)
	bus.Subscribe(domainevents.KindOptimizationApplied, sub.handle)
	return sub
}

func (s *Subscriber) handle(ev domainevents.Event) {
	if s.closed.Load() {
		atomicAdd(&s.drops)
		return
	}
	entry, ok := entryFromEvent(ev, s.actor)
	if !ok {
		return
	}
	select {
	case s.sem <- struct{}{}:
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := s.rec.Record(ctx, entry); err != nil {
				s.logger.Warn("audit: record from domain event", "kind", ev.Kind(), "err", err)
			}
		}()
	default:
		atomicAdd(&s.drops)
		s.logger.Warn("audit: backpressure drop", "kind", ev.Kind())
	}
}

// Close stops accepting events and waits for in-flight recorder
// goroutines to finish. Safe to call multiple times.
func (s *Subscriber) Close() {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.wg.Wait()
}

// DroppedCount returns events shed due to backpressure since the
// subscriber was created.
func (s *Subscriber) DroppedCount() int64 {
	if s == nil {
		return 0
	}
	return atomicLoad(&s.drops)
}

func entryFromEvent(ev domainevents.Event, actor string) (Entry, bool) {
	switch e := ev.(type) {
	case domainevents.BudgetExceeded:
		return Entry{
			Action:    ActionBudgetExceeded,
			Actor:     actor,
			Target:    e.BudgetID,
			Timestamp: e.At,
			Details: map[string]any{
				"spent_usd": e.SpentUSD,
				"limit_usd": e.LimitUSD,
				"fraction":  ratio(e.SpentUSD, e.LimitUSD),
			},
		}, true
	case domainevents.OptimizationApplied:
		return Entry{
			Action:    ActionOptimizationApply,
			Actor:     actor,
			Target:    e.OptimizerKind,
			Timestamp: e.At,
			Details: map[string]any{
				"prompt_hash":  e.PromptHash,
				"tokens_saved": e.TokensSaved,
			},
		}, true
	default:
		return Entry{}, false
	}
}

func ratio(a, b float64) string {
	if b == 0 {
		return "0"
	}
	return fmt.Sprintf("%.3f", a/b)
}
