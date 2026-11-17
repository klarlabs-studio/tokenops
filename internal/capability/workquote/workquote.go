package workquote

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	capb "go.klarlabs.de/tokenops/internal/capability/boundaries"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/work/quote"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Domain event kinds this package records.
const (
	KindQuote  = "work.quote"
	KindActual = "work.quote_actual"
)

// StatusNoBasis means the history a quote is drawn from has not been
// computed yet.
const StatusNoBasis quote.Status = "no_basis"

const (
	source = "work_quote"
	// prepareLookback bounds how long after its quote a task can still be
	// checked against it.
	prepareLookback = 30 * 24 * time.Hour
	// calibrationLookback bounds the checked quotes calibration reads.
	calibrationLookback = 90 * 24 * time.Hour
	queryLimit          = 100_000
)

// Store is the canonical local event store.
type Store interface {
	Append(ctx context.Context, env *eventschema.Envelope) error
	Query(ctx context.Context, f sqlite.Filter) ([]*eventschema.Envelope, error)
}

// Request is the task about to start.
type Request struct {
	WorkflowID string
	// Instruction is classified in memory for its kind of work and never
	// stored.
	Instruction string
	Client      string
	Provider    string
	Model       string
	Effort      string
}

// Deps wires Prepare.
type Deps struct {
	// Basis is the stored history; nil when none was computed yet.
	Basis *Basis
	// Reports are the plans' current windows.
	Reports []plans.HeadroomReport
	// Rules are the boundaries in force; Env reads the figures the daemon
	// keeps for them.
	Rules []domain.Rule
	Env   capb.Env
	// Store records the quote so review can check it; nil records nothing.
	Store Store
}

// Calibrations is how often quoted tasks stayed within their p90, by what
// was measured: the cost, where the task's turns carried its workflow,
// and the share of the plan window, where the plan's meter was read at
// both ends.
type Calibrations struct {
	Cost  quote.Calibration `json:"cost"`
	Share quote.Calibration `json:"window_share"`
}

// Result is the quote a task gets before it starts.
type Result struct {
	quote.Quote
	// BasisComputedAt is when the history was last computed.
	BasisComputedAt time.Time `json:"basis_computed_at,omitzero"`
	// Boundaries are the rules a p90 task would meet.
	Boundaries []capb.Trip `json:"boundaries,omitempty"`
	// NeedsApproval is an enforced rule that would ask someone at the
	// p90; Refused one that would refuse. Approval says how it is given.
	NeedsApproval bool   `json:"needs_approval"`
	Refused       bool   `json:"refused"`
	Approval      string `json:"approval,omitempty"`
	// Calibration shows how earlier quotes fared; read it before
	// trusting the p90.
	Calibration Calibrations `json:"calibration"`
	// Recorded says the quote was stored, so review can check it.
	Recorded bool `json:"recorded"`
}

// Prepare quotes the task from the basis, against the plan's current
// windows and the boundaries in force, and records the quote.
func Prepare(ctx context.Context, d Deps, req Request, now time.Time) (*Result, error) {
	want := quote.Want{Client: strings.TrimSpace(req.Client), Model: strings.TrimSpace(req.Model), Effort: strings.TrimSpace(req.Effort)}
	if k := taskclass.ClassifyKind(req.Instruction); k != taskclass.KindUnknown {
		want.Kind = string(k)
	}
	out := &Result{}
	if d.Store != nil {
		c, err := Calibration(ctx, d.Store, now)
		if err != nil {
			return nil, err
		}
		out.Calibration = c
	}
	if d.Basis == nil {
		out.Quote = quote.Quote{Status: StatusNoBasis, Estimate: true, Want: want, MinSamples: quote.MinSamples,
			LookbackDays: int(quote.Lookback / (24 * time.Hour)), Caveat: quote.Caveat,
			Summary: "no quote yet: the daemon has not computed the history a quote is drawn from; it does a few minutes after it starts, then whenever new work is recorded"}
		return out, nil
	}
	out.BasisComputedAt = d.Basis.ComputedAt
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = quote.Build(d.Basis.Samples, want, nil, now).Provider
	}
	windows := currentWindows(d.Basis, d.Reports, provider, now)
	out.Quote = quote.Build(d.Basis.Samples, want, windows, now)
	if out.Provider == "" {
		out.Provider = provider
	}
	if out.Quoted() {
		out.Boundaries = capb.QuoteTrips(d.Rules, d.Env, d.Reports, provider, p90Shares(out.Windows), out.P90USD)
		judgeTrips(out)
	}
	if d.Store != nil && strings.TrimSpace(req.WorkflowID) != "" && out.Quoted() {
		if err := appendRecord(ctx, d.Store, KindQuote, quoteRecordOf(req.WorkflowID, out.Quote, windows), now); err != nil {
			return nil, err
		}
		out.Recorded = true
	}
	return out, nil
}

// currentWindows are provider's windows now, each with its measured rate.
func currentWindows(b *Basis, reports []plans.HeadroomReport, provider string, now time.Time) []quote.Window {
	var out []quote.Window
	for _, r := range reports {
		if r.Provider != provider {
			continue
		}
		for _, w := range r.Windows {
			if w.Name == "" || !w.Current(now) {
				continue
			}
			used := w.UsedPct
			if w.NotStarted {
				used = 0
			}
			qw := quote.Window{Provider: provider, Name: w.Name, Duration: w.Duration, UsedPct: used}
			if !w.ResetsAt.IsZero() {
				qw.ResetsIn = w.ResetsAt.Sub(now)
			}
			if rate, ok := b.rateFor(provider, w.Name, w.Duration); ok {
				qw.USDPerPct, qw.CalibratedOn = rate.USDPerPct, rate.Windows
				qw.Duration = rate.Duration
			}
			out = append(out, qw)
		}
	}
	return out
}

// p90Shares are the p90 shares of the windows boundaries name.
func p90Shares(ws []quote.WindowShare) map[domain.Window]float64 {
	out := map[domain.Window]float64{}
	for _, w := range ws {
		if w.Known && (w.Window == string(domain.Window5h) || w.Window == string(domain.WindowWeek)) {
			out[domain.Window(w.Window)] = w.P90Pct
		}
	}
	return out
}

// judgeTrips sums the trips up: whether the operator must approve, or a
// rule would refuse, and says so in the summary.
func judgeTrips(r *Result) {
	var named []string
	for _, t := range r.Boundaries {
		if !t.Enforced {
			continue
		}
		r.NeedsApproval = r.NeedsApproval || t.NeedsApproval
		r.Refused = r.Refused || t.Refuses
		named = append(named, fmt.Sprintf("%s (%s: %s)", t.Rule, t.Require, t.Crossed))
	}
	if len(named) == 0 {
		return
	}
	switch {
	case r.Refused:
		r.Approval = "A boundary refuses work past this point at the p90. Tell the operator before starting; only they can change it."
	default:
		r.Approval = "At the p90 a boundary needs approval. Ask the operator before starting: they answer through the boundaries flow " +
			"(the prompt when the rule is met, `tokenops boundaries`, or tokenops_boundaries on their word); an agent never approves its own."
	}
	r.Summary += "; at the p90 it would meet boundary " + strings.Join(named, ", ")
}

// quoteRecord is the KindQuote body: figures only, never the words.
type quoteRecord struct {
	WorkflowID string         `json:"workflow_id"`
	Client     string         `json:"client,omitempty"`
	Provider   string         `json:"provider,omitempty"`
	Model      string         `json:"model"`
	Effort     string         `json:"effort,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Basis      quote.Basis    `json:"basis"`
	Samples    int            `json:"samples"`
	P50USD     float64        `json:"p50_usd"`
	P90USD     float64        `json:"p90_usd"`
	Windows    []recordWindow `json:"windows,omitempty"`
}

// recordWindow is one window's quoted p90 share.
type recordWindow struct {
	Window          string  `json:"window"`
	DurationSeconds int64   `json:"duration_seconds,omitempty"`
	P90Pct          float64 `json:"p90_pct"`
}

func quoteRecordOf(workflowID string, q quote.Quote, windows []quote.Window) quoteRecord {
	rec := quoteRecord{WorkflowID: workflowID, Client: q.Want.Client, Provider: q.Provider, Model: q.Want.Model,
		Effort: q.Want.Effort, Kind: q.Want.Kind, Basis: q.Basis, Samples: q.Samples, P50USD: q.P50USD, P90USD: q.P90USD}
	for i, w := range q.Windows {
		if !w.Known || i >= len(windows) {
			continue
		}
		rec.Windows = append(rec.Windows, recordWindow{Window: w.Window,
			DurationSeconds: int64(windows[i].Duration.Seconds()), P90Pct: w.P90Pct})
	}
	return rec
}

// Actual is what a finished task took, as review measured it.
type Actual struct {
	// CostUSD is the task's turns at list prices, when they carried its
	// workflow.
	CostUSD      float64
	CostMeasured bool
	// SharePct is the share of the plan window of Window length the task
	// took, read from the plan's meter at both ends.
	Window        time.Duration
	SharePct      float64
	ShareMeasured bool
}

// actualRecord is the KindActual body.
type actualRecord struct {
	WorkflowID string        `json:"workflow_id"`
	Cost       *quote.Scored `json:"cost,omitempty"`
	Share      *quote.Scored `json:"window_share,omitempty"`
	Window     string        `json:"window,omitempty"`
}

// Check is a quote set against what the task took.
type Check struct {
	P50USD float64 `json:"p50_usd"`
	P90USD float64 `json:"p90_usd"`
	// Cost and Share are absent when that figure was not measured.
	Cost        *quote.Scored `json:"cost,omitempty"`
	Share       *quote.Scored `json:"window_share,omitempty"`
	ShareWindow string        `json:"window,omitempty"`
	Summary     string        `json:"summary"`
	Calibration Calibrations  `json:"calibration"`
	Recorded    bool          `json:"recorded"`
}

// Review sets the quote workflowID was given against what it took, and
// records the result, which calibration reads. It returns nil when the
// workflow was never quoted.
func Review(ctx context.Context, store Store, workflowID string, a Actual, now time.Time) (*Check, error) {
	if store == nil || strings.TrimSpace(workflowID) == "" {
		return nil, nil
	}
	recs, _, err := domainRecords[quoteRecord](ctx, store, KindQuote, now.Add(-prepareLookback))
	if err != nil {
		return nil, err
	}
	var q *quoteRecord
	for i := range recs {
		if recs[i].WorkflowID == workflowID {
			q = &recs[i]
			break
		}
	}
	if q == nil {
		return nil, nil
	}
	out := &Check{P50USD: q.P50USD, P90USD: q.P90USD}
	var parts []string
	if a.CostMeasured {
		out.Cost = &quote.Scored{P90: q.P90USD, Actual: round2(a.CostUSD)}
		parts = append(parts, fmt.Sprintf("cost $%.2f against a p90 of $%.2f (%s)", out.Cost.Actual, q.P90USD, withinWords(*out.Cost)))
	}
	if a.ShareMeasured {
		for _, w := range q.Windows {
			if time.Duration(w.DurationSeconds)*time.Second == a.Window {
				out.Share, out.ShareWindow = &quote.Scored{P90: w.P90Pct, Actual: a.SharePct}, w.Window
				parts = append(parts, fmt.Sprintf("took %.1f%% of the %s window against a p90 of %.1f%% (%s)",
					a.SharePct, w.Window, w.P90Pct, withinWords(*out.Share)))
				break
			}
		}
	}
	if len(parts) == 0 {
		out.Summary = "quoted, but neither the task's cost nor its share of the plan window was measured, so the quote cannot be checked"
	} else {
		out.Summary = "estimate check: " + strings.Join(parts, "; ")
		rec := actualRecord{WorkflowID: workflowID, Cost: out.Cost, Share: out.Share, Window: out.ShareWindow}
		if err := appendRecord(ctx, store, KindActual, rec, now); err != nil {
			return nil, err
		}
		out.Recorded = true
	}
	c, err := Calibration(ctx, store, now)
	if err != nil {
		return nil, err
	}
	out.Calibration = c
	return out, nil
}

func withinWords(s quote.Scored) string {
	if s.Within() {
		return "within"
	}
	return "over"
}

// Calibration reads how often the quoted tasks checked in the last 90
// days stayed within their p90, the latest check per workflow.
func Calibration(ctx context.Context, store Store, now time.Time) (Calibrations, error) {
	if store == nil {
		return Calibrations{Cost: quote.Calibrate(nil), Share: quote.Calibrate(nil)}, nil
	}
	recs, _, err := domainRecords[actualRecord](ctx, store, KindActual, now.Add(-calibrationLookback))
	if err != nil {
		return Calibrations{}, err
	}
	latest := map[string]actualRecord{}
	var order []string
	for _, r := range recs {
		if _, seen := latest[r.WorkflowID]; !seen {
			order = append(order, r.WorkflowID)
		}
		latest[r.WorkflowID] = r
	}
	var cost, share []quote.Scored
	for _, id := range order {
		r := latest[id]
		if r.Cost != nil {
			cost = append(cost, *r.Cost)
		}
		if r.Share != nil {
			share = append(share, *r.Share)
		}
	}
	return Calibrations{Cost: quote.Calibrate(cost), Share: quote.Calibrate(share)}, nil
}

func appendRecord(ctx context.Context, store Store, kind string, body any, now time.Time) error {
	env, err := eventschema.NewDomainEnvelope(kind, body, now, source, eventschema.Association{})
	if err != nil {
		return err
	}
	return store.Append(ctx, env)
}

// domainRecords decodes every recorded body of kind since the cut-off,
// oldest first.
func domainRecords[T any](ctx context.Context, store Store, kind string, since time.Time) ([]T, []time.Time, error) {
	envs, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeDomain, Source: source, Since: since, Limit: queryLimit})
	if err != nil {
		return nil, nil, err
	}
	var (
		out []T
		at  []time.Time
	)
	for _, env := range envs {
		d, ok := env.Payload.(*eventschema.DomainEvent)
		if !ok || d.Kind != kind {
			continue
		}
		var rec T
		if json.Unmarshal(d.Data, &rec) != nil {
			continue
		}
		out = append(out, rec)
		at = append(at, env.Timestamp)
	}
	return out, at, nil
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }
