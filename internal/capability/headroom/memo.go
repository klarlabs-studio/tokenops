package headroom

import (
	"context"
	"sort"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// memoReader answers every ReadEvents of one computation from a single
// read per event type, filtered in memory.
//
// One headroom answer asks the store for the same weeks of events many
// times over: per provider, for its consumption, window, spend, vendor
// windows and inferred bindings. Nineteen reads of up to a month each
// made a glance take two seconds on an idle store, and ten times that
// in a daemon busy ingesting.
type memoReader struct {
	r Reader
	// floor is the earliest moment any question here asks about.
	floor time.Time

	mu     sync.Mutex
	events map[eventschema.EventType]*memoEvents
	counts map[[2]time.Time]map[string]int64
}

// memoEvents is one type's events from the floor on, as the store gave them.
type memoEvents struct {
	all []*eventschema.Envelope
	// sorted is whether all is in time order with no nil, so a read from a
	// moment is a suffix of it, found by search rather than by a filter.
	sorted bool
}

// inTimeOrder reports whether events are all set and in time order.
func inTimeOrder(events []*eventschema.Envelope) bool {
	for i, e := range events {
		if e == nil || (i > 0 && e.Timestamp.Before(events[i-1].Timestamp)) {
			return false
		}
	}
	return true
}

// memoFloor is how far back a headroom computation reads: the start of the
// month or two weeks back (vendor windows), whichever is earlier.
func memoFloor(now time.Time) time.Time {
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	twoWeeks := now.Add(-14 * 24 * time.Hour)
	if twoWeeks.Before(month) {
		return twoWeeks
	}
	return month
}

// EventsFloor is how far back a glance at now reads events: a cache filled
// from it ahead of time answers the whole glance. That is the memo's floor
// or the plan advice's replay (plans.AdviceDays back), whichever is
// earlier; the advice reads through the same reader, outside the memo.
func EventsFloor(now time.Time) time.Time {
	advice := now.Add(-time.Duration(plans.AdviceDays) * 24 * time.Hour)
	if f := memoFloor(now); f.Before(advice) {
		return f
	}
	return advice
}

// memoize switches the memo off, for a test comparing answers with and
// without it.
var memoize = true

func newMemoReader(r Reader, now time.Time) Reader {
	if r == nil || !memoize {
		return r
	}
	if _, already := r.(*memoReader); already {
		return r
	}
	return &memoReader{r: r, floor: memoFloor(now), events: map[eventschema.EventType]*memoEvents{}, counts: map[[2]time.Time]map[string]int64{}}
}

// LatestAttributesBySource and AttributesBySourceBetween pass a meter's
// readings through from the reader underneath, uncached, so the run-out
// forecast reads them through the memo; a reader without them has none.
func (m *memoReader) LatestAttributesBySource(ctx context.Context, source, key string, since time.Time) (map[string]string, time.Time, bool, error) {
	if h, ok := m.r.(AttributeReader); ok {
		return h.LatestAttributesBySource(ctx, source, key, since)
	}
	return nil, time.Time{}, false, nil
}

func (m *memoReader) AttributesBySourceBetween(ctx context.Context, source, key string, since, until time.Time) (map[string]string, time.Time, bool, error) {
	if h, ok := m.r.(HistoryReader); ok {
		return h.AttributesBySourceBetween(ctx, source, key, since, until)
	}
	return nil, time.Time{}, false, nil
}

// ReadEvents answers from the floor's read. The events a question gets
// are shared with the memo and with every other question: callers must not
// modify the slice, and the slice's capacity is its length, so an append
// copies it. A read the memo holds in time order costs a search, not a copy
// of the month.
func (m *memoReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	if since.Before(m.floor) {
		// Older than anything memoised: ask the store.
		return m.r.ReadEvents(ctx, t, since)
	}
	m.mu.Lock()
	held, ok := m.events[t]
	m.mu.Unlock()
	if !ok {
		all, err := m.r.ReadEvents(ctx, t, m.floor)
		if err != nil {
			return nil, err
		}
		held = &memoEvents{all: all, sorted: inTimeOrder(all)}
		m.mu.Lock()
		m.events[t] = held
		m.mu.Unlock()
	}
	if held.sorted {
		from := sort.Search(len(held.all), func(i int) bool { return !held.all[i].Timestamp.Before(since) })
		if from == len(held.all) {
			return []*eventschema.Envelope{}, nil
		}
		return held.all[from:len(held.all):len(held.all)], nil
	}
	out := make([]*eventschema.Envelope, 0, len(held.all))
	for _, e := range held.all {
		if e != nil && !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memoReader) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	key := [2]time.Time{since, until}
	m.mu.Lock()
	c, ok := m.counts[key]
	m.mu.Unlock()
	if ok {
		return c, nil
	}
	c, err := m.r.CountBySource(ctx, since, until)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.counts[key] = c
	m.mu.Unlock()
	return c, nil
}
