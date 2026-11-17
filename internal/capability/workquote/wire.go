package workquote

import (
	"context"
	"math"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventReader streams the events a filter matches.
type EventReader interface {
	EachMatching(ctx context.Context, f sqlite.Filter, fn func(*eventschema.Envelope) error) error
}

// InputsFor reads this machine's transcripts (every client's default
// root), the turns agg prices and the plan window readings in events.
// The transcripts are read with their words, which Samples classifies in
// memory and drops.
func InputsFor(agg *analytics.Aggregator, events EventReader) Inputs {
	in := Inputs{
		Units: func(_ context.Context, since, now time.Time) ([]agentdx.Unit, error) {
			days := max(1, int(math.Ceil(now.Sub(since).Hours()/24)))
			return sessions.Units(sessions.Window{Days: days}, now)
		},
	}
	if agg != nil {
		in.Turns = func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
			return agg.SessionTurns(ctx, analytics.Filter{Since: since})
		}
	}
	if events != nil {
		in.Windows = func(ctx context.Context, since, now time.Time) (map[string][]plans.QuotaWindow, error) {
			var peaks plans.WindowPeaks
			for _, src := range plans.WindowSources() {
				err := events.EachMatching(ctx, sqlite.Filter{Source: src, Since: since, Until: now}, func(e *eventschema.Envelope) error {
					peaks.Add(e)
					return nil
				})
				if err != nil {
					return nil, err
				}
			}
			return peaks.Readings(), nil
		}
	}
	return in
}
