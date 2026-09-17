// Package retention enforces configurable retention windows on the
// local SQLite event store. Each event type has its own keep-for
// duration; rows older than now-window are pruned by a periodic
// scheduler.
//
// A window may also be scoped to one source. Every vendor-usage reader
// writes type "prompt", so without that scope a single window governs
// Claude Code, Codex, opencode, Cursor and Copilot together, and the
// imported history of a client used months ago cannot be kept without
// also keeping today's live stream. A source window overrides the type
// window for its own rows and leaves every other source on the type
// rule. The audit_log table is intentionally not retention-
// managed — operators rely on it for forensic queries weeks or months
// after the fact.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Policy maps an event type — optionally narrowed to one source — to its
// retention window. A zero or negative window disables pruning for what
// the policy covers.
type Policy struct {
	EventType eventschema.EventType
	// Source narrows the policy to events from one reader ("opencode",
	// "codex-jsonl", ...). Empty applies to every source that no other
	// policy claims.
	Source  string
	KeepFor time.Duration
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
	Source     string
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
	// Sources that carry their own window are excluded from their type's
	// window. Without that exclusion the broader type rule would delete
	// the very rows a source rule was configured to keep, and the
	// operator would see a setting that reads as applied but does
	// nothing.
	claimed := claimedSources(p.cfg.Policies)
	out := make([]PruneResult, 0, len(p.cfg.Policies))
	for _, pol := range p.cfg.Policies {
		if pol.KeepFor <= 0 {
			continue
		}
		cutoff := now.Add(-pol.KeepFor).UTC()
		query, args := deleteStatement(pol, cutoff, claimed[pol.EventType])
		res, err := p.store.DB().ExecContext(ctx, query, args...)
		if err != nil {
			return out, fmt.Errorf("retention: delete %s: %w", pol.describe(), err)
		}
		n, _ := res.RowsAffected()
		out = append(out, PruneResult{
			Source:     pol.Source,
			EventType:  pol.EventType,
			CutoffTime: cutoff,
			Deleted:    n,
		})
		if n > 0 {
			deleted += n
			p.cfg.Logger.Info("retention prune",
				"type", pol.EventType,
				"source", logSource(pol.Source),
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
	// Checkpoint regardless of what was deleted: the WAL grows with
	// writes, not with retention, so gating this on a deletion leaves the
	// busiest stores — the ones that most need it — never checkpointed.
	if err := p.checkpointWAL(ctx); err != nil {
		p.cfg.Logger.Warn("wal checkpoint failed", "err", err)
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

// claimedSources indexes, per event type, the sources that carry their
// own policy.
func claimedSources(pols []Policy) map[eventschema.EventType]map[string]struct{} {
	out := make(map[eventschema.EventType]map[string]struct{})
	for _, pol := range pols {
		if pol.Source == "" {
			continue
		}
		if out[pol.EventType] == nil {
			out[pol.EventType] = make(map[string]struct{})
		}
		out[pol.EventType][pol.Source] = struct{}{}
	}
	return out
}

// deleteStatement builds the prune for one policy. A source-scoped policy
// deletes only its own rows; a type-wide policy skips every source that
// has a policy of its own, including one that keeps its rows forever.
func deleteStatement(pol Policy, cutoff time.Time, claimed map[string]struct{}) (string, []any) {
	args := []any{string(pol.EventType), cutoff.UnixNano()}
	if pol.Source != "" {
		return `DELETE FROM events WHERE type = ? AND timestamp_ns < ? AND source = ?`,
			append(args, pol.Source)
	}
	if len(claimed) == 0 {
		return `DELETE FROM events WHERE type = ? AND timestamp_ns < ?`, args
	}
	// Sorted so the statement is stable across passes and readable in a
	// query log.
	names := make([]string, 0, len(claimed))
	for s := range claimed {
		names = append(names, s)
	}
	sort.Strings(names)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	for _, n := range names {
		args = append(args, n)
	}
	return `DELETE FROM events WHERE type = ? AND timestamp_ns < ? ` +
		`AND COALESCE(source, '') NOT IN (` + placeholders + `)`, args
}

func (p Policy) describe() string {
	if p.Source == "" {
		return string(p.EventType)
	}
	return string(p.EventType) + "/" + p.Source
}

func logSource(s string) string {
	if s == "" {
		return "(all others)"
	}
	return s
}

// checkpointWAL folds the write-ahead log back into the database and
// truncates it.
//
// SQLite checkpoints automatically at ~1000 pages, but only up to the
// oldest snapshot any reader still holds. A store that is read
// continuously — a dashboard, a scheduler, a poller — can therefore keep
// the automatic checkpoint from ever resetting the log, and the -wal file
// grows without limit. Once it is large enough, an ordinary batch insert
// stops fitting in its timeout and the writer gives up on it.
//
// TRUNCATE is the mode that actually returns the space; PASSIVE would
// leave the file at its high-water mark. A busy checkpoint is not an
// error worth reporting: it means a reader held the lock and the next
// pass will try again.
func (p *Pruner) checkpointWAL(ctx context.Context) error {
	var busy, logFrames, checkpointed int
	row := p.store.DB().QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err := row.Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("retention: wal checkpoint: %w", err)
	}
	if busy != 0 {
		p.cfg.Logger.Debug("wal checkpoint busy; retrying next pass")
		return nil
	}
	if checkpointed > 0 {
		p.cfg.Logger.Info("wal checkpoint", "frames", checkpointed)
	}
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
