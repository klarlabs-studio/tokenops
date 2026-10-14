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
// readings.
type EventReader interface {
	Query(ctx context.Context, f sqlite.Filter) ([]*eventschema.Envelope, error)
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

// maxEvents bounds each read of recorded events.
const maxEvents = 50_000

// DefaultDays is the period when none is asked for.
const DefaultDays = 7

// Compute builds the report for [since, until) against the BaselineDays
// before since. A zero until is now.
func Compute(ctx context.Context, d Deps, since, until time.Time) (Report, error) {
	if until.IsZero() {
		until = time.Now()
	}
	start := BaselineSince(since)
	w, err := commits.Gather(ctx, commits.Deps{Turns: d.Turns, Dirs: d.Dirs, Repos: d.Repos}, start)
	if err != nil {
		return Report{}, err
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
		in.Outcomes, err = d.Events.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeOutcome, Since: start, Until: until, Limit: maxEvents})
		if err != nil {
			return Report{}, err
		}
		var readings []*eventschema.Envelope
		for _, src := range plans.WindowSources() {
			envs, err := d.Events.Query(ctx, sqlite.Filter{Source: src, Since: start, Until: until, Limit: maxEvents})
			if err != nil {
				return Report{}, err
			}
			readings = append(readings, envs...)
		}
		in.Windows = plans.WindowReadings(readings)
	}
	return Build(in), nil
}
