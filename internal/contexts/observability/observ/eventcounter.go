package observ

import (
	"maps"
	"sort"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventCounter is a thread-safe per-kind counter that observes canonical
// domain envelopes. Dashboards / MCP tools / health probes consume Counts().
type EventCounter struct {
	mu     sync.RWMutex
	counts map[string]int64
	spans  map[string]KindSpan
}

// KindSpan is when the counted events of one kind happened: the earliest
// and the latest. The daemon hydrates the counter from its canonical store
// at boot, so counts are lifetime totals; without the span, 274 budget
// alerts from a budget deleted in June read as 274 happening now.
type KindSpan struct {
	First time.Time `json:"first_at"`
	Last  time.Time `json:"last_at"`
}

// NewEventCounter returns an empty counter.
func NewEventCounter() *EventCounter {
	return &EventCounter{counts: map[string]int64{}, spans: map[string]KindSpan{}}
}

// SubscribeCanonical observes accepted canonical envelopes. Call once after
// hydrating persisted history and before event producers start.
func (c *EventCounter) SubscribeCanonical(bus events.Observable) func() {
	if bus == nil {
		return func() {}
	}
	return bus.Subscribe(func(env *eventschema.Envelope) {
		if env == nil || env.Type != eventschema.EventTypeDomain {
			return
		}
		payload, ok := env.Payload.(*eventschema.DomainEvent)
		if !ok {
			return
		}
		c.observe(payload.Kind, env.Timestamp.UTC())
	})
}

// Hydrate restores the lifetime counter from canonical domain envelopes.
func (c *EventCounter) Hydrate(envelopes []*eventschema.Envelope) {
	for _, env := range envelopes {
		if env == nil || env.Type != eventschema.EventTypeDomain {
			continue
		}
		p, ok := env.Payload.(*eventschema.DomainEvent)
		if !ok || p.Kind == "" {
			continue
		}
		c.observe(p.Kind, env.Timestamp.UTC())
	}
}

func (c *EventCounter) observe(kind string, at time.Time) {
	if kind == "" {
		return
	}
	c.mu.Lock()
	c.counts[kind]++
	sp := c.spans[kind]
	if sp.First.IsZero() || at.Before(sp.First) {
		sp.First = at
	}
	if at.After(sp.Last) {
		sp.Last = at
	}
	c.spans[kind] = sp
	c.mu.Unlock()
}

// Counts returns a snapshot of per-kind counts as a map. Map iteration
// is unordered — use Kinds() for a deterministically-sorted slice when
// stable ordering is needed. Safe to call concurrently with bus
// publication.
func (c *EventCounter) Counts() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int64, len(c.counts))
	maps.Copy(out, c.counts)
	return out
}

// Spans returns when each kind's counted events happened.
func (c *EventCounter) Spans() map[string]KindSpan {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]KindSpan, len(c.spans))
	maps.Copy(out, c.spans)
	return out
}

// Kinds returns a deterministic, sorted slice of every kind the counter
// has observed. Useful for stable rendering in tables.
func (c *EventCounter) Kinds() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.counts))
	for k := range c.counts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CountEntry is one row of SortedCounts.
type CountEntry struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// SortedCounts returns per-kind counts ordered alphabetically. Use
// instead of Counts when stable ordering matters (dashboards, JSON
// snapshots, golden tests).
func (c *EventCounter) SortedCounts() []CountEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]string, 0, len(c.counts))
	for k := range c.counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]CountEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, CountEntry{Kind: k, Count: c.counts[k]})
	}
	return out
}

// Reset clears every accumulated count. Returned to callers from
// bootstrap.Shutdown so a re-used Components instance (in tests) sees
// a fresh counter.
func (c *EventCounter) Reset() {
	c.mu.Lock()
	c.counts = map[string]int64{}
	c.mu.Unlock()
}

// Total returns the sum across every kind.
func (c *EventCounter) Total() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var total int64
	for _, v := range c.counts {
		total += v
	}
	return total
}
