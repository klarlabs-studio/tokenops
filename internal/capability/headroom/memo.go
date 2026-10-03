package headroom

import (
	"context"
	"sync"
	"time"

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
	events map[eventschema.EventType][]*eventschema.Envelope
	counts map[[2]time.Time]map[string]int64
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
	return &memoReader{r: r, floor: memoFloor(now), events: map[eventschema.EventType][]*eventschema.Envelope{}, counts: map[[2]time.Time]map[string]int64{}}
}

func (m *memoReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	if since.Before(m.floor) {
		// Older than anything memoised: ask the store.
		return m.r.ReadEvents(ctx, t, since)
	}
	m.mu.Lock()
	all, ok := m.events[t]
	m.mu.Unlock()
	if !ok {
		var err error
		if all, err = m.r.ReadEvents(ctx, t, m.floor); err != nil {
			return nil, err
		}
		m.mu.Lock()
		m.events[t] = all
		m.mu.Unlock()
	}
	out := make([]*eventschema.Envelope, 0, len(all))
	for _, e := range all {
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
