package avoidable

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Deps are the readers the report draws on. Nil readers leave their
// class (or the realized side) out.
type Deps struct {
	Turns func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)
	// Dirs maps sessions to where they ran; nil reads the clients'
	// defaults.
	Dirs  func(since time.Time) map[string]sessiondirs.Dir
	Repos *gitlog.Repos
	// Events reads outcomes and plan window readings.
	Events uniteconomics.EventReader
	// Trace reconstructs a workflow for the waste detector, configured by
	// Waste.
	Trace func(ctx context.Context, workflowID string) (*workflow.Trace, error)
	Waste waste.Config
	Price Pricer
	// Rereads are the read guard's redundant re-reads that went ahead and
	// those it refused.
	Rereads func() (wentAhead, refused []Reread)
	// Routes are the coach's model advice.
	Routes func() []RouteAdvice
	// Savings reads the realized-savings ledger over [since, now) and
	// Steps each guardrail's graduation.
	Savings func(ctx context.Context, since, now time.Time) savings.Report
	Steps   func(ctx context.Context, now time.Time) []savings.Step
}

// maxEvents bounds each read of recorded events.
const maxEvents = 50_000

// Compute builds the report for [since, until). A zero until is now.
func Compute(ctx context.Context, d Deps, since, until time.Time) (Report, error) {
	if until.IsZero() {
		until = time.Now()
	}
	start := since.AddDate(0, 0, -CalibrationDays)
	w, err := commits.Gather(ctx, commits.Deps{Turns: d.Turns, Dirs: d.Dirs, Repos: d.Repos}, start)
	if err != nil {
		return Report{}, err
	}
	in := Inputs{Since: since, Until: until, Dirs: w.Dirs, RootOf: w.RootOf, CommitsOf: w.CommitsOf,
		Waste: d.Waste, Price: d.Price, Traces: map[string]*workflow.Trace{}, Windows: map[string][]plans.QuotaWindow{},
		Savings: savings.Report{RealizedUSD: measurement.Unknown("the savings ledger was not read")}}
	for _, t := range w.Turns {
		if t.At.Before(until) {
			in.Turns = append(in.Turns, t)
		}
	}
	if d.Events != nil {
		if in.Outcomes, err = d.Events.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeOutcome, Since: start, Until: until, Limit: maxEvents}); err != nil {
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
	if d.Trace != nil {
		for _, id := range workflowsIn(in) {
			if err := ctx.Err(); err != nil {
				return Report{}, err
			}
			if tr, err := d.Trace(ctx, id); err == nil {
				in.Traces[id] = tr
			}
		}
	}
	if d.Rereads != nil {
		in.Rereads, in.Refused = d.Rereads()
	}
	if d.Routes != nil {
		in.Routes = d.Routes()
	}
	if d.Savings != nil {
		in.Savings = d.Savings(ctx, since, until)
	}
	if d.Steps != nil {
		in.Steps = d.Steps(ctx, until)
	}
	return Build(in), nil
}

// workflowsIn are the workflows with at least two turns in the period:
// what the waste detector has a pattern to find in.
func workflowsIn(in Inputs) []string {
	n := map[string]int{}
	var out []string
	for _, t := range in.Turns {
		if t.WorkflowID == "" || t.At.Before(in.Since) || !t.At.Before(in.Until) {
			continue
		}
		if n[t.WorkflowID]++; n[t.WorkflowID] == 2 {
			out = append(out, t.WorkflowID)
		}
	}
	return out
}
