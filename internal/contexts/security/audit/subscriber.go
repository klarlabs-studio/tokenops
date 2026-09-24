package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// Subscriber is the long-lived audit handle wired into the canonical event bus.
// DroppedCount reflects events shed due to backpressure since the
// subscriber was created; useful for dashboards / health probes.
type Subscriber struct {
	rec    *Recorder
	logger *slog.Logger
	actor  string
	sem    chan struct{}
	drops  int64
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
	detach func()
}

// Subscribe attaches the audit recorder to bus so the audit log captures
// security-relevant domain events (budget breaches, applied
// optimizations) without each publisher knowing about audit. The
// recorder runs in a bounded goroutine pool — excess events are dropped
// and counted instead of spawning unbounded goroutines.
func Subscribe(bus events.Observable, rec *Recorder, logger *slog.Logger, actor string) *Subscriber {
	return SubscribeWithOptions(bus, rec, logger, SubscribeOptions{Actor: actor})
}

// SubscribeWithOptions is the configurable form of Subscribe.
func SubscribeWithOptions(bus events.Observable, rec *Recorder, logger *slog.Logger, opts SubscribeOptions) *Subscriber {
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
	sub.detach = bus.Subscribe(sub.handle)
	return sub
}

func (s *Subscriber) handle(env *eventschema.Envelope) {
	if env == nil || env.Type != eventschema.EventTypeDomain {
		return
	}
	payload, ok := env.Payload.(*eventschema.DomainEvent)
	if !ok {
		return
	}
	entry, ok := entryFromEnvelope(env, s.actor)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.closed {
		atomicAdd(&s.drops)
		s.mu.Unlock()
		return
	}
	select {
	case s.sem <- struct{}{}:
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := s.rec.Record(ctx, entry); err != nil {
				s.logger.Warn("audit: record from canonical event", "kind", payload.Kind, "err", err)
			}
		}()
	default:
		atomicAdd(&s.drops)
		s.mu.Unlock()
		s.logger.Warn("audit: backpressure drop", "kind", payload.Kind)
	}
}

// Close stops accepting events and waits for in-flight recorder
// goroutines to finish. Safe to call multiple times.
func (s *Subscriber) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.detach != nil {
		s.detach()
	}
	s.mu.Unlock()
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

func entryFromEnvelope(env *eventschema.Envelope, actor string) (Entry, bool) {
	if env == nil || env.Type != eventschema.EventTypeDomain {
		return Entry{}, false
	}
	payload, ok := env.Payload.(*eventschema.DomainEvent)
	if !ok {
		return Entry{}, false
	}
	switch payload.Kind {
	case "budget.exceeded":
		var e struct {
			BudgetID string  `json:"BudgetID"`
			SpentUSD float64 `json:"SpentUSD"`
			LimitUSD float64 `json:"LimitUSD"`
		}
		if err := json.Unmarshal(payload.Data, &e); err != nil {
			return Entry{}, false
		}
		return Entry{
			Action:    ActionBudgetExceeded,
			Actor:     actor,
			Target:    e.BudgetID,
			Timestamp: env.Timestamp,
			Details: map[string]any{
				"spent_usd": e.SpentUSD,
				"limit_usd": e.LimitUSD,
				"fraction":  ratio(e.SpentUSD, e.LimitUSD),
			},
		}, true
	case "optimization.applied":
		var e struct {
			PromptHash    string `json:"PromptHash"`
			OptimizerKind string `json:"OptimizerKind"`
			TokensSaved   int64  `json:"TokensSaved"`
		}
		if err := json.Unmarshal(payload.Data, &e); err != nil {
			return Entry{}, false
		}
		return Entry{
			Action:    ActionOptimizationApply,
			Actor:     actor,
			Target:    e.OptimizerKind,
			Timestamp: env.Timestamp,
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
