package daemon

import (
	"context"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// sourceFreshnessFn builds the callback GET /api/sources serves.
//
// The daemon is the only process that knows all three facts at once. It
// owns the event store, it constructs the pollers, and it can read the
// local origins — where `tokenops serve` (the MCP server) and a dashboard
// are different processes entirely. Exposing the assessment over the API
// is what lets them answer "is this still working" without each
// re-deriving it from a database they may not have.
//
// The callback is evaluated per request rather than cached: a status
// answer that is minutes old is the failure mode this whole phase is
// about.
func sourceFreshnessFn(cfg config.Config, store state.SourceCounter, health *state.SourceRegistry, sup *lifecycle.Supervisor) func() []state.SourceReport {
	if store == nil {
		return nil
	}
	store = carryLastSeen(store)
	return func() []state.SourceReport {
		// A status query must not hang behind a busy database. Answering
		// late is the same as not answering.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return state.AssessSources(ctx, cfg, store, health, stoppedReaders(sup), time.Now())
	}
}

// lastSeenCarry answers the last-event lookup with the previous answer
// when the store misses its deadline. A source's last event only moves
// forward, so the previous answer is a true lower bound; losing it made
// every source read as never seen on the next assessment. With nothing
// to carry, the failure is passed on and reported as unknown.
type lastSeenCarry struct {
	state.SourceCounter

	mu   sync.Mutex
	last map[string]time.Time
}

func carryLastSeen(store state.SourceCounter) *lastSeenCarry {
	return &lastSeenCarry{SourceCounter: store}
}

func (c *lastSeenCarry) LastEventBySource(ctx context.Context) (map[string]time.Time, error) {
	seen, err := c.SourceCounter.LastEventBySource(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if c.last == nil {
			return nil, err
		}
		return c.last, nil
	}
	c.last = seen
	return seen, nil
}

// stoppedReaders reports which supervised readers have exited. A reader
// that exited is the strongest signal there is, and it only became
// knowable once the pollers ran under a supervisor. The task names are the
// source tags on purpose, so a supervisor failure lines up with the source
// it belongs to without a translation table.
//
// Nothing could answer this before: the daemon started nine pollers with
// a bare `go func()`, so one dying logged a warning and vanished while
// every status surface went on reporting the daemon healthy.
func stoppedReaders(sup *lifecycle.Supervisor) map[string]error {
	if sup == nil {
		return nil
	}
	return sup.Failed()
}

// How old an assessment may be before it is redone, and how often the
// daemon redoes it unasked. An assessment runs every store query and
// origin check again; on a loaded machine that took 10–15 seconds, past
// the 2-second limit `tokenops status` and the MCP status tool allow, so
// both fell back to the event counts and never saw a refused reader.
const (
	sourceSnapshotMaxAge  = 2 * time.Minute
	sourceSnapshotRefresh = 30 * time.Second
)

// sourceSnapshot keeps the latest source assessment, so GET /api/sources
// and the daemon's status answer from it at once. It is never served
// past maxAge: a reader asking then gets a new assessment, and readers
// arriving meanwhile wait for that one rather than starting their own.
type sourceSnapshot struct {
	assess func() []state.SourceReport
	maxAge time.Duration
	now    func() time.Time

	run sync.Mutex // one assessment at a time

	mu      sync.Mutex
	reports []state.SourceReport
	at      time.Time
}

func newSourceSnapshot(assess func() []state.SourceReport, maxAge time.Duration) *sourceSnapshot {
	return &sourceSnapshot{assess: assess, maxAge: maxAge, now: time.Now}
}

// Reports is the latest assessment, redone first when it is too old.
func (s *sourceSnapshot) Reports() []state.SourceReport {
	if r, ok := s.current(); ok {
		return r
	}
	s.run.Lock()
	defer s.run.Unlock()
	if r, ok := s.current(); ok {
		return r
	}
	return s.store(s.assess())
}

// Refresh redoes the assessment now.
func (s *sourceSnapshot) Refresh() {
	s.run.Lock()
	defer s.run.Unlock()
	s.store(s.assess())
}

func (s *sourceSnapshot) current() ([]state.SourceReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at.IsZero() || s.now().Sub(s.at) > s.maxAge {
		return nil, false
	}
	return s.reports, true
}

func (s *sourceSnapshot) store(r []state.SourceReport) []state.SourceReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports, s.at = r, s.now()
	return r
}

// sourceSnapshotFn wraps the assessment in a snapshot the supervisor
// refreshes every sourceSnapshotRefresh, so a status caller finds one
// ready. Nil in, nil out: no store means the route stays unmounted.
func sourceSnapshotFn(assess func() []state.SourceReport, sup *lifecycle.Supervisor) func() []state.SourceReport {
	if assess == nil {
		return nil
	}
	snap := newSourceSnapshot(assess, sourceSnapshotMaxAge)
	if sup != nil {
		sup.Go("source-freshness", func(ctx context.Context) error {
			t := time.NewTicker(sourceSnapshotRefresh)
			defer t.Stop()
			for {
				snap.Refresh()
				select {
				case <-ctx.Done():
					return nil
				case <-t.C:
				}
			}
		})
	}
	return snap.Reports
}
