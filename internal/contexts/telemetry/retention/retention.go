// Package retention enforces configurable retention windows on the
// local SQLite event store. Each event type has its own keep-for
// duration; rows older than now-window are pruned by a periodic
// scheduler. The audit_log table is intentionally not retention-
// managed — operators rely on it for forensic queries weeks or months
// after the fact.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Policy maps an event type to its retention window. A zero or
// negative window disables pruning for that type.
type Policy struct {
	EventType eventschema.EventType
	KeepFor   time.Duration
}

// Config bundles the per-type policies.
type Config struct {
	Policies []Policy
	// Interval is how often the scheduler wakes to prune. Default 1h.
	Interval time.Duration
	// Logger receives prune logs (rows deleted, errors).
	Logger *slog.Logger
	// Reclaim runs a VACUUM after a pass that actually deleted rows, so
	// the freed pages return to the filesystem instead of sitting on
	// SQLite's freelist. Without it the database file keeps its
	// high-water mark forever and pruning frees nothing an operator can
	// see. Off by default: VACUUM rewrites the whole database and holds
	// a write lock for the duration.
	Reclaim bool
}

// PruneResult reports the outcome of one Run pass.
type PruneResult struct {
	EventType  eventschema.EventType
	CutoffTime time.Time
	Deleted    int64
}

// Pruner deletes events older than each policy's window.
type Pruner struct {
	store *sqlite.Store
	cfg   Config
	clock func() time.Time
}

// New constructs a Pruner. cfg.Interval defaults to 1h.
func New(store *sqlite.Store, cfg Config) *Pruner {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Pruner{store: store, cfg: cfg, clock: time.Now}
}

// SetClock overrides time.Now (tests).
func (p *Pruner) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	p.clock = now
}

// Run executes one prune pass across every configured policy. Returns
// the per-policy result. A nil receiver or empty policy slice is a
// no-op.
func (p *Pruner) Run(ctx context.Context) ([]PruneResult, error) {
	if p == nil || p.store == nil {
		return nil, errors.New("retention: pruner not initialised")
	}
	now := p.clock()
	var deleted int64
	out := make([]PruneResult, 0, len(p.cfg.Policies))
	for _, pol := range p.cfg.Policies {
		if pol.KeepFor <= 0 {
			continue
		}
		cutoff := now.Add(-pol.KeepFor).UTC()
		res, err := p.store.DB().ExecContext(ctx,
			`DELETE FROM events WHERE type = ? AND timestamp_ns < ?`,
			string(pol.EventType), cutoff.UnixNano(),
		)
		if err != nil {
			return out, fmt.Errorf("retention: delete %s: %w", pol.EventType, err)
		}
		n, _ := res.RowsAffected()
		out = append(out, PruneResult{
			EventType:  pol.EventType,
			CutoffTime: cutoff,
			Deleted:    n,
		})
		if n > 0 {
			deleted += n
			p.cfg.Logger.Info("retention prune",
				"type", pol.EventType,
				"deleted", n,
				"cutoff", cutoff.Format(time.RFC3339),
			)
		}
	}
	if p.cfg.Reclaim && deleted > 0 {
		if err := p.reclaim(ctx); err != nil {
			// The prune itself succeeded; failing to shrink the file is
			// not worth failing the pass over.
			p.cfg.Logger.Warn("retention reclaim failed", "err", err)
		}
	}
	return out, nil
}

// reclaim returns freed pages to the filesystem. auto_vacuum cannot be
// switched on for an existing database without a full rewrite, so a
// plain VACUUM is the only option that works on a store already in the
// field. It is called only after a pass that deleted rows.
func (p *Pruner) reclaim(ctx context.Context) error {
	before, _ := p.freelistCount(ctx)
	if before == 0 {
		return nil
	}
	if _, err := p.store.DB().ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("retention: vacuum: %w", err)
	}
	p.cfg.Logger.Info("retention reclaim", "pages_freed", before)
	return nil
}

func (p *Pruner) freelistCount(ctx context.Context) (int64, error) {
	var n int64
	err := p.store.DB().QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&n)
	return n, err
}

// Scheduler runs Pruner.Run on cfg.Interval until ctx is cancelled.
type Scheduler struct {
	pruner *Pruner
	once   sync.Once
	done   chan struct{}
}

// NewScheduler returns a Scheduler. Start spawns the goroutine; Stop
// cancels via the supplied context.
func NewScheduler(p *Pruner) *Scheduler {
	return &Scheduler{pruner: p, done: make(chan struct{})}
}

// Start begins the periodic loop. The first prune fires immediately so
// freshly-started daemons reclaim space without waiting an Interval.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		defer close(s.done)
		if _, err := s.pruner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.pruner.cfg.Logger.Error("retention initial prune", "err", err)
		}
		ticker := time.NewTicker(s.pruner.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.pruner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					s.pruner.cfg.Logger.Error("retention prune", "err", err)
				}
			}
		}
	}()
}

// Wait blocks until the scheduler goroutine exits. Idempotent.
func (s *Scheduler) Wait() {
	s.once.Do(func() { <-s.done })
}
