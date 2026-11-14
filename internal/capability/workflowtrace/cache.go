package workflowtrace

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// TraceCache keeps reconstructed workflows between reads, and
// reconstructs one again only when one of its prompt events was stored,
// updated or deleted since, or the rate card changed (ADR 0019).
//
// The waste report reconstructs every workflow of its period, each read
// and decoded from the store and priced, on every hourly team upload;
// most of those workflows ended days ago. A kept trace answers after a
// check of a few indexed rows.
//
// The traces it returns are shared between reads and must not be
// modified. It forgets a trace not read for idle.
type TraceCache struct {
	store *sqlite.Store
	spend *spend.Engine
	idle  time.Duration
	now   func() time.Time

	mu      sync.Mutex
	entries map[string]*traceEntry
	// rebuilt counts the traces reconstructed, for tests.
	rebuilt int
}

// traceEntry is one workflow's trace and what it was built from.
type traceEntry struct {
	mark     sqlite.WorkflowMark
	revision uint64
	trace    *workflow.Trace
	// err is ErrNoTrace when the store held nothing for the workflow.
	err  error
	used time.Time
}

// DefaultTraceIdle is how long a TraceCache keeps a trace nobody read:
// past the hourly upload's interval.
const DefaultTraceIdle = 2 * time.Hour

// NewTraceCache reconstructs from store, priced by spendEng, keeping
// traces for idle (DefaultTraceIdle when zero).
func NewTraceCache(store *sqlite.Store, spendEng *spend.Engine, idle time.Duration) *TraceCache {
	if idle <= 0 {
		idle = DefaultTraceIdle
	}
	return &TraceCache{store: store, spend: spendEng, idle: idle, now: time.Now, entries: map[string]*traceEntry{}}
}

// Reconstruct returns what workflow.Reconstruct returns for id.
func (c *TraceCache) Reconstruct(ctx context.Context, id string) (*workflow.Trace, error) {
	if c.store == nil || id == "" {
		return workflow.Reconstruct(ctx, c.store, c.spend, id)
	}
	now := c.now()
	revision := c.spend.Revision()
	c.mu.Lock()
	c.forget(now)
	e := c.entries[id]
	var held sqlite.WorkflowMark
	if e != nil {
		held = e.mark
	}
	c.mu.Unlock()
	if e != nil && e.revision == revision {
		mark, same, err := c.store.WorkflowUnchanged(ctx, id, held)
		if err != nil && !sqlite.IsNoChangeLog(err) {
			return nil, err
		}
		if same {
			c.mu.Lock()
			e.mark, e.used = mark, now
			c.mu.Unlock()
			return e.trace, e.err
		}
	}
	// Marked before the read: a change in between is a change next time.
	mark, err := c.store.MarkWorkflow(ctx, id)
	if err != nil {
		if sqlite.IsNoChangeLog(err) {
			return workflow.Reconstruct(ctx, c.store, c.spend, id)
		}
		return nil, err
	}
	trace, err := workflow.Reconstruct(ctx, c.store, c.spend, id)
	if err != nil && !errors.Is(err, workflow.ErrNoTrace) {
		return nil, err
	}
	c.mu.Lock()
	c.rebuilt++
	c.entries[id] = &traceEntry{mark: mark, revision: revision, trace: trace, err: err, used: now}
	c.mu.Unlock()
	return trace, err
}

// forget drops the traces not read since idle before now. c.mu is held.
func (c *TraceCache) forget(now time.Time) {
	for id, e := range c.entries {
		if now.Sub(e.used) > c.idle {
			delete(c.entries, id)
		}
	}
}
