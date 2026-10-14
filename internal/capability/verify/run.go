package verify

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventReader is the store the comparison reads its events from.
type EventReader interface {
	Query(ctx context.Context, f sqlite.Filter) ([]*eventschema.Envelope, error)
}

// Window selects the work compared: the transcripts the attempts are
// reconstructed from, and the events recorded over the same days.
type Window struct {
	// Root is the transcript root; empty reads each client's default.
	Root string
	// Days is the window; All reads every transcript and event and
	// overrides it.
	Days int
	All  bool
	// IdleGap is the pause that starts a new attempt; zero takes the
	// default.
	IdleGap time.Duration
	// Savings, when set, adds the coach's holdout cohorts from the
	// realized-savings ledger to the report.
	Savings *savings.Deps
}

// maxEvents bounds the events one comparison reads.
const maxEvents = 200_000

// Run reconstructs the attempts in w, reads the events over the same
// window, and compares them; experimentID picks one trial when several
// are present. readErr names transcripts that could not be read: what the
// other clients yielded is still compared, so it is not fatal.
//
// `tokenops verify` and tokenops_verify each assembled this themselves,
// and the tool could not ask for every transcript the way the command
// could: its "0 reads everything" meant 30 days.
func Run(ctx context.Context, events EventReader, w Window, experimentID string, now time.Time) (report Report, readErr error, err error) {
	units, readErr := sessions.Units(sessions.Window{Root: w.Root, Days: w.Days, All: w.All}, now)
	reconstructed := reconstruct.FromUnits(units, reconstruct.Options{IdleGap: w.IdleGap})
	filter := sqlite.Filter{Limit: maxEvents}
	if !w.All && w.Days > 0 {
		filter.Since = now.AddDate(0, 0, -w.Days)
	}
	envs, err := events.Query(ctx, filter)
	if err != nil {
		return Report{}, readErr, err
	}
	report = CompareReconstructedExperiment(reconstructed, envs, experimentID)
	if w.Savings != nil {
		report.Holdout = savings.Build(ctx, *w.Savings, now).Holdouts()
	}
	return report, readErr, nil
}
