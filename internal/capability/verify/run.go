package verify

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/work"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventReader is the store the comparison reads its events from: every
// event a filter matches, oldest first.
type EventReader interface {
	EachMatching(ctx context.Context, f sqlite.Filter, fn func(*eventschema.Envelope) error) error
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
	var since time.Time
	if !w.All && w.Days > 0 {
		since = now.AddDate(0, 0, -w.Days)
	}
	report, err = CompareStored(ctx, events, reconstructed, since, experimentID)
	if err != nil {
		return Report{}, readErr, err
	}
	if w.Savings != nil {
		report.Holdout = savings.Build(ctx, *w.Savings, now).Holdouts()
	}
	return report, readErr, nil
}

// CompareStored compares rs against every event recorded at or after
// since (zero reads them all), as CompareReconstructedExperiment does.
//
// A busy store holds hundreds of thousands of events in a period, and a
// capped read dropped the oldest without saying so. The events are
// streamed instead, and only those that can bear on the comparison are
// kept, so the read covers the whole period without holding it.
func CompareStored(ctx context.Context, events EventReader, rs []reconstruct.Work, since time.Time, experimentID string) (Report, error) {
	relevant := bearsOn(rs)
	var kept []*eventschema.Envelope
	// The store drops the rows bearsOn would drop on their columns alone,
	// so they are never decoded; bearsOn still checks the time of the rest.
	f := sqlite.Filter{Since: since, Linked: true, Actors: actorsOf(rs)}
	err := events.EachMatching(ctx, f, func(env *eventschema.Envelope) error {
		if relevant(env) {
			kept = append(kept, env)
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	return CompareReconstructedExperiment(rs, kept, experimentID), nil
}

// actorsOf lists the distinct actors that ran an execution in rs, the only
// actors whose events can join an attempt by time.
func actorsOf(rs []reconstruct.Work) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		by := string(r.Execution.By)
		if by != "" && !seen[by] {
			seen[by] = true
			out = append(out, by)
		}
	}
	return out
}

// bearsOn reports whether an event can change the comparison of rs.
//
// An event linked to an execution or an experiment can: it may be an
// attempt's, an assignment, route evidence, or name an execution no
// transcript recorded. Any other event joins an attempt only by its
// actor and time, as Attribute joins it; one that joins none is counted
// nowhere and is dropped.
func bearsOn(rs []reconstruct.Work) func(*eventschema.Envelope) bool {
	byActor := make(map[string][]work.Execution)
	for _, r := range rs {
		if by := string(r.Execution.By); by != "" {
			byActor[by] = append(byActor[by], r.Execution)
		}
	}
	return func(env *eventschema.Envelope) bool {
		if env == nil {
			return false
		}
		if env.Association.Execution != "" || env.Correlation.Experiment != "" {
			return true
		}
		for _, e := range byActor[env.Association.Actor] {
			if within(e, env) {
				return true
			}
		}
		return false
	}
}
