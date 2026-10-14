package avoidable

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	ftstore "go.klarlabs.de/tokenops/internal/infra/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// DepsFor wires the report to this machine: the turns through agg, the
// event store for outcomes, plan windows and the waste detector's
// workflows, the rate card in eng, the read guard's and the coach's
// ledgers under the home directory, and the coach's configuration in cfg
// for each guardrail's stage. A nil store or engine leaves out what needs
// it.
func DepsFor(cfg config.Config, agg *analytics.Aggregator, store *sqlite.Store, eng *spend.Engine) Deps {
	d := Deps{
		Turns:   commits.TurnsIn(agg, analytics.Filter{}),
		Waste:   cfg.Coaching.WasteConfig(),
		Rereads: readGuardLedger,
		Routes:  modelAdvice,
		Savings: periodSavings,
		Steps: func(ctx context.Context, now time.Time) []savings.Step {
			var history []ft.Summary
			if l, err := ftstore.Default(); err == nil {
				history = coach.History(l, now)
			}
			return savings.Graduations(savings.Build(ctx, coach.SavingsDeps(), now), coach.Build(cfg).Effective, history)
		},
	}
	if store != nil {
		d.Events = store
		d.Trace = func(ctx context.Context, id string) (*workflow.Trace, error) {
			return workflow.Reconstruct(ctx, store, eng, id)
		}
	}
	if eng != nil {
		d.Price = func(u Usage, at time.Time) (float64, bool) {
			v, err := eng.ComputeAt(&eventschema.PromptEvent{
				Provider: eventschema.Provider(u.Provider), RequestModel: u.Model,
				InputTokens: u.Input, CachedInputTokens: u.CacheRead, CacheWriteInputTokens: u.CacheWrite,
				CacheWrite1hInputTokens: u.CacheWrite1h, OutputTokens: u.Output,
			}, at)
			return v, err == nil
		}
	}
	return d
}

// readGuardLedger reads the guard's re-reads: those that went ahead and
// those it refused. An unreadable ledger reads as none.
func readGuardLedger() (wentAhead, refused []Reread) {
	conv := func(rs []readguard.Reclamation) []Reread {
		out := make([]Reread, 0, len(rs))
		for _, r := range rs {
			out = append(out, Reread{At: r.At, Session: r.SessionID, Tokens: r.Tokens})
		}
		return out
	}
	if rs, err := readguard.RedundantReads(""); err == nil {
		wentAhead = conv(rs)
	}
	if rs, err := readguard.Reclamations(""); err == nil {
		refused = conv(rs)
	}
	return wentAhead, refused
}

// modelAdvice is the coach's advice to move work to a cheaper model, from
// the follow-through ledger.
func modelAdvice() []RouteAdvice {
	l, err := ftstore.Default()
	if err != nil {
		return nil
	}
	entries, err := l.Load()
	if err != nil {
		return nil
	}
	return routesIn(entries)
}

// routesIn are the model offers among entries.
func routesIn(entries []ft.Entry) []RouteAdvice {
	var out []RouteAdvice
	for _, e := range entries {
		if e.Type == ft.EntryOffer && e.Power == config.PowerModels && e.From != "" && e.To != "" && e.Session != "" {
			out = append(out, RouteAdvice{At: e.At, Session: e.Session, Kind: e.Kind, From: e.From, To: e.To})
		}
	}
	return out
}

// periodSavings is the savings ledger read back over the interventions
// made in [since, until), with their outcomes whenever they came.
func periodSavings(ctx context.Context, since, until time.Time) savings.Report {
	d := coach.SavingsDeps()
	if d.Ledger != nil {
		d.Ledger = periodLedger{Ledger: d.Ledger, since: since, until: until}
	}
	return savings.Build(ctx, d, until)
}

// periodLedger reads the interventions of one period, and every outcome:
// an outcome is matched to its intervention by ID.
type periodLedger struct {
	savings.Ledger
	since, until time.Time
}

func (p periodLedger) Load() ([]learning.SavingsEntry, error) {
	all, err := p.Ledger.Load()
	if err != nil {
		return nil, err
	}
	out := make([]learning.SavingsEntry, 0, len(all))
	for _, e := range all {
		if e.Type == learning.SavingsIntervention && (e.At.Before(p.since) || !e.At.Before(p.until)) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
