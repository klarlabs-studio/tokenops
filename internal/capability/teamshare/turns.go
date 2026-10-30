package teamshare

import (
	"context"
	"slices"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// An upload's sections each need the event store's turns over a span
// ending now: the buckets the covered days, the units of work a week
// more for pull requests, the waste report four weeks more for its
// baseline. Read separately, the same rows were scanned and priced three
// times an upload, and on a busy daemon the reads ran the upload past its
// deadline. One read of the widest span now serves them all.

// TurnReader reads the priced turns since a time.
type TurnReader func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)

// sharedTurns serves the reads of one upload from a single read. It is
// not safe for concurrent use; Build reads its sections in turn.
type sharedTurns struct {
	read  TurnReader
	since time.Time
	turns []analytics.SessionTurn // oldest first, as the event store returns them
	held  bool
}

// prime reads the turns since since and holds them.
func (s *sharedTurns) prime(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
	turns, err := s.read(ctx, since)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].At.Before(turns[j].At) })
	s.since, s.turns, s.held = since, turns, true
	return turns, nil
}

// Read is the turns since since: from those held when they reach back
// that far, else read. Each caller gets its own copy.
func (s *sharedTurns) Read(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
	if !s.held || since.Before(s.since) {
		return s.read(ctx, since)
	}
	return slices.Clone(turnsSince(s.turns, since)), nil
}

// turnsSince is the tail of turns, oldest first, at or after since.
func turnsSince(turns []analytics.SessionTurn, since time.Time) []analytics.SessionTurn {
	i := sort.Search(len(turns), func(i int) bool { return !turns[i].At.Before(since) })
	return turns[i:]
}

// turnSpan is where the one read of an upload's turns starts: the
// earliest any section it builds reads from.
func turnSpan(d Deps, opts Options, first time.Time) time.Time {
	start := first
	if opts.Schema >= teamwire.SchemaSpend && d.Schema3.Placed != nil {
		start = first.Add(-uniteconomics.PRMaxGap)
	}
	if opts.Schema >= teamwire.SchemaFinOps && d.FinOps.Waste != nil {
		if w := first.AddDate(0, 0, -avoidable.CalibrationDays); w.Before(start) {
			start = w
		}
	}
	return start
}
