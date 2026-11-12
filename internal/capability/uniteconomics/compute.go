package uniteconomics

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventReader reads recorded events: the outcomes and the plan window
// readings. It hands over every event the filter matches, without a cap:
// a figure over a period is computed over all of the period.
type EventReader interface {
	EachMatching(ctx context.Context, f sqlite.Filter, fn func(*eventschema.Envelope) error) error
}

// Deps are the readers the report draws on.
type Deps struct {
	Turns func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)
	// Dirs maps sessions to where they ran; nil reads the clients'
	// defaults.
	Dirs  func(since time.Time) map[string]sessiondirs.Dir
	Repos *gitlog.Repos
	// Events reads outcomes and window readings; nil leaves outcomes and
	// plan shares out.
	Events EventReader
}

// DefaultDays is the period when none is asked for.
const DefaultDays = 7

// Compute builds the report for [since, until) against the BaselineDays
// before since. A zero until is now.
func Compute(ctx context.Context, d Deps, since, until time.Time) (Report, error) {
	in, err := Gather(ctx, d, BaselineSince(since), since, until)
	if err != nil {
		return Report{}, err
	}
	return Build(in), nil
}

// Gather reads what the joins need for [since, until), with turns,
// commits and pull requests from start: the baseline's start for the
// report, a week before since for PlacedUnits. A zero until is now.
func Gather(ctx context.Context, d Deps, start, since, until time.Time) (Inputs, error) {
	if until.IsZero() {
		until = time.Now()
	}
	w, err := commits.Gather(ctx, commits.Deps{Turns: d.Turns, Dirs: d.Dirs, Repos: d.Repos}, start)
	if err != nil {
		return Inputs{}, err
	}
	in := Inputs{Since: since, Until: until, Dirs: w.Dirs, RootOf: w.RootOf, CommitsOf: w.CommitsOf,
		PRsOf: map[string][]gitlog.PR{}, Windows: map[string][]plans.QuotaWindow{}}
	for _, t := range w.Turns {
		if t.At.Before(until) {
			in.Turns = append(in.Turns, t)
		}
	}
	for _, root := range w.Roots() {
		in.PRsOf[root], _ = w.Repos.MergedPRs(ctx, root, start)
	}
	if d.Events != nil {
		if in.Outcomes, in.Windows, err = ReadRecorded(ctx, d.Events, start, until); err != nil {
			return Inputs{}, err
		}
	}
	return in, nil
}

// ReadRecorded reads the outcomes and the plan window readings recorded
// in [start, until): every outcome, and each window at its peak. The
// window readings are taken in as they are read, so a busy period's
// thousands of readings of the same window are never all held.
func ReadRecorded(ctx context.Context, r EventReader, start, until time.Time) ([]*eventschema.Envelope, map[string][]plans.QuotaWindow, error) {
	var outcomes []*eventschema.Envelope
	err := r.EachMatching(ctx, sqlite.Filter{Type: eventschema.EventTypeOutcome, Since: start, Until: until},
		func(e *eventschema.Envelope) error {
			outcomes = append(outcomes, e)
			return nil
		})
	if err != nil {
		return nil, nil, err
	}
	var peaks plans.WindowPeaks
	for _, src := range plans.WindowSources() {
		err := r.EachMatching(ctx, sqlite.Filter{Source: src, Since: start, Until: until},
			func(e *eventschema.Envelope) error {
				peaks.Add(e)
				return nil
			})
		if err != nil {
			return nil, nil, err
		}
	}
	return outcomes, peaks.Readings(), nil
}
