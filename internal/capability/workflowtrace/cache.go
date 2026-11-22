package workflowtrace

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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
// It keeps each workflow's events packed (sqlite.Packer), not its trace:
// a trace holds every step's decoded envelope, and the traces of a
// fortnight of workflows were 210 MB of the daemon's live heap for the
// two hours they are kept. A kept workflow's trace is built again from
// its packed events on each read, priced at the same rate card, so it is
// the trace a reconstruction gives; building it costs no read and no
// decoding. The traces it returns are the caller's; their steps'
// envelopes carry the attributes the event cache keeps (the waste report
// reads none), in sets shared between steps that must not be modified. It
// forgets a workflow not read for idle.
type TraceCache struct {
	store *sqlite.Store
	spend *spend.Engine
	idle  time.Duration
	now   func() time.Time

	mu      sync.Mutex
	entries map[string]*traceEntry
	// packer packs the events kept, under mu.
	packer *sqlite.Packer
	// rebuilt counts the traces reconstructed, for tests.
	rebuilt int
}

// traceEntry is one workflow's events, packed, and the mark and rate
// card its trace was built at.
type traceEntry struct {
	mark     sqlite.WorkflowMark
	revision uint64
	events   []sqlite.PackedEvent
	// err is ErrNoTrace when the store held nothing for the workflow.
	err  error
	used time.Time
}

// trace builds e's trace, priced by eng.
func (e *traceEntry) trace(id string, eng *spend.Engine) (*workflow.Trace, error) {
	if e.err != nil {
		return nil, e.err
	}
	envs := make([]*eventschema.Envelope, len(e.events))
	for i, p := range e.events {
		envs[i] = p.Envelope()
	}
	return workflow.FromEvents(id, envs, eng)
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
	return &TraceCache{store: store, spend: spendEng, idle: idle, now: time.Now, entries: map[string]*traceEntry{}, packer: sqlite.NewPacker()}
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
			return e.trace(id, c.spend)
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
	envs, err := workflow.Events(ctx, c.store, id)
	if err != nil {
		return nil, err
	}
	// Packed first: packing leaves out the attributes the kept events do
	// not hold, so this trace is the one later reads build.
	c.mu.Lock()
	packed := make([]sqlite.PackedEvent, len(envs))
	for i, env := range envs {
		packed[i] = c.packer.Pack(env)
	}
	c.mu.Unlock()
	trace, err := workflow.FromEvents(id, envs, c.spend)
	if err != nil && !errors.Is(err, workflow.ErrNoTrace) {
		return nil, err
	}
	c.mu.Lock()
	c.rebuilt++
	c.entries[id] = &traceEntry{mark: mark, revision: revision, events: packed, err: err, used: now}
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
