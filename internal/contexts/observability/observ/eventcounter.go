package observ

import (
	"maps"
	"sort"
	"sync"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
)

// EventCounter is a thread-safe per-kind counter that subscribes to a
// domain bus and increments on every published event. Dashboards / MCP
// tools / health probes consume Counts() to surface in-process event
// volumes without round-tripping through storage.
type EventCounter struct {
	mu     sync.RWMutex
	counts map[string]int64
}

// NewEventCounter returns an empty counter.
func NewEventCounter() *EventCounter {
	return &EventCounter{counts: map[string]int64{}}
}

// Subscribe wires the counter to bus on every event kind (wildcard).
// Call once at daemon boot.
func (c *EventCounter) Subscribe(bus *domainevents.Bus) {
	if bus == nil {
		return
	}
	bus.Subscribe("*", func(ev domainevents.Event) {
		c.mu.Lock()
		c.counts[ev.Kind()]++
		c.mu.Unlock()
	})
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
