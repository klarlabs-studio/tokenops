// Package budgets watches the configured budgets: each tick it measures
// every budget's window, projects the rest of it, and reports what is new
// since the last tick, with the models the price list cannot cost.
//
// It is the daemon's active-mode watcher, out of the daemon: the
// adapter decides only how often to tick and where findings go.
package budgets

import (
	"context"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/auditlog"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Source is the spend rollup the watcher measures with;
// *analytics.Aggregator satisfies it.
type Source interface {
	Summarize(ctx context.Context, f analytics.Filter) (analytics.Summary, error)
	AggregateBy(ctx context.Context, f analytics.Filter, b analytics.Bucket, g analytics.Group) ([]analytics.Row, error)
}

// Publisher takes the budget.exceeded domain events.
type Publisher interface {
	Publish(*eventschema.Envelope)
}

// Limit is one configured budget.
type Limit = budget.Limit

// Alert is a budget finding: a threshold reached or a breach forecast.
type Alert = budget.Alert

// Unpriced is a model the price list cannot cost.
type Unpriced = analytics.UnpricedModel

// Watcher reports each finding once.
type Watcher struct {
	Source Source
	Limits []Limit
	// Publish, when set, takes a budget.exceeded event the first time a
	// budget's window reaches its limit.
	Publish Publisher
	// Recorded reports whether budget window key (Limit.Key) was already recorded as
	// exceeded since the start of its window. The watcher's own memory
	// ends with the process, and every config write restarts the daemon;
	// without this each restart recorded the same exceedance again. Nil
	// means nothing is recorded.
	Recorded func(ctx context.Context, name string, since time.Time) bool

	seen map[string]bool
}

// Report is what a tick found that earlier ticks had not.
type Report struct {
	Alerts   []Alert
	Unpriced []Unpriced
	// Errors are the reads that failed; their budgets were skipped.
	Errors []error
}

// forecastHistory is how much history a forecast trains on, whatever the
// budget window, so a short window still gets a trend.
const forecastHistory = 14 * 24 * time.Hour

// Tick measures every budget at now.
func (w *Watcher) Tick(ctx context.Context, now time.Time) Report {
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	var r Report
	alerts := budget.EvaluateAll(w.Limits,
		func(l Limit) float64 {
			v, err := Measure(ctx, w.Source, l, now)
			if err != nil {
				r.Errors = append(r.Errors, err)
			}
			return v
		},
		func(l Limit) []forecast.Prediction { return w.forecast(ctx, l, now) },
	)
	for _, a := range alerts {
		start := budget.WindowStart(a.Limit.Window, now)
		// Before the alert's own dedupe: a budget critical at 96% keeps
		// that alert's key when it reaches 100%, and is exceeded then.
		w.publishExceeded(ctx, a, start, now)
		key := fmt.Sprintf("%s|%s|%s|%s", a.Limit.Key(), a.Kind, a.Severity, start.Format(time.RFC3339))
		if w.seen[key] {
			continue
		}
		w.seen[key] = true
		r.Alerts = append(r.Alerts, a)
	}
	s, err := w.Source.Summarize(ctx, analytics.Filter{Since: now.Add(-24 * time.Hour)})
	if err != nil {
		r.Errors = append(r.Errors, fmt.Errorf("unpriced models: %w", err))
		return r
	}
	for _, u := range s.Unpriced {
		key := "unpriced|" + u.Provider + "|" + u.Model
		if w.seen[key] {
			continue
		}
		w.seen[key] = true
		r.Unpriced = append(r.Unpriced, u)
	}
	return r
}

// TagSource measures the work carrying one allocation tag ("key=value").
// A Source that is one can measure a budget scoped to a tag.
type TagSource interface {
	SummarizeTag(ctx context.Context, f analytics.Filter, tag string) (analytics.Summary, error)
}

// Measure is what l's metric reached in its current window at now.
func Measure(ctx context.Context, src Source, l Limit, now time.Time) (float64, error) {
	f := analytics.Filter{
		Since: budget.WindowStart(l.Window, now), Until: now,
		WorkflowID: l.WorkflowID, AgentID: l.AgentID,
	}
	var (
		s   analytics.Summary
		err error
	)
	if l.Tag != "" {
		ts, ok := src.(TagSource)
		if !ok {
			return 0, fmt.Errorf("budget %s: scoped to %s, which only allocation rules can measure", l.Name, l.Tag)
		}
		s, err = ts.SummarizeTag(ctx, f, l.Tag)
	} else {
		s, err = src.Summarize(ctx, f)
	}
	if err != nil {
		return 0, fmt.Errorf("budget %s: %w", l.Name, err)
	}
	switch l.Basis {
	case budget.BasisTokens:
		return float64(s.TotalTokens), nil
	case budget.BasisEquivalent:
		return s.APIEquivalentUSD, nil
	default:
		return s.CostUSD, nil
	}
}

// Reading is one budget window measured.
type Reading = boundaryledger.BudgetReading

// Read measures each budget window at now. A window that cannot be
// measured, or has no limit, is left out: a missing figure never
// satisfies a boundary, and a surface shows nothing rather than zero.
func Read(ctx context.Context, src Source, limits []Limit, now time.Time) []Reading {
	var out []Reading
	for _, l := range limits {
		limit := l.Threshold()
		if limit <= 0 {
			continue
		}
		v, err := Measure(ctx, src, l, now)
		if err != nil {
			continue
		}
		warn, crit := l.Thresholds()
		start := budget.WindowStart(l.Window, now)
		mode := l.Mode
		if mode == "" {
			mode = budget.ModeSoft
		}
		basis := l.Basis
		if basis == "" {
			basis = budget.BasisSpend
		}
		out = append(out, Reading{
			Key: l.Key(), Budget: l.Name, Window: string(l.Window), Mode: mode, Basis: basis,
			Used: v, Limit: limit, UsedPct: v / limit * 100,
			WarnPct: warn * 100, CritPct: crit * 100,
			WindowStart: start, ResetsAt: budget.WindowEnd(l.Window, start),
		})
	}
	return out
}

// FreshFor is how long the daemon's last reading of the budget windows
// counts for the coach and the glance. The daemon measures them at least
// every five minutes while it runs.
const FreshFor = 15 * time.Minute

// Current is the budget windows the daemon last measured into dir (empty
// for the default), or nil when it has not measured them lately: a stale
// figure is not shown as the present one.
func Current(dir boundaryledger.Dir, now time.Time) []Reading {
	fig, ok, err := dir.GetFigures()
	if err != nil || !ok || now.Sub(fig.ObservedAt) > FreshFor {
		return nil
	}
	return fig.Budgets
}

// UsedPctOf is the share of each budget window used, by key.
// A budget with several windows also gets an entry under its own name:
// its fullest window, so a rule naming the budget holds on any of them.
func UsedPctOf(readings []Reading) map[string]float64 {
	out := map[string]float64{}
	for _, r := range readings {
		out[r.Key] = r.UsedPct
		if r.Key != r.Budget {
			if cur, ok := out[r.Budget]; !ok || r.UsedPct > cur {
				out[r.Budget] = r.UsedPct
			}
		}
	}
	return out
}

// UsedPct is the share of each budget window used in its current window,
// by key (see UsedPctOf).
func UsedPct(ctx context.Context, src Source, limits []Limit, now time.Time) map[string]float64 {
	return UsedPctOf(Read(ctx, src, limits, now))
}

// forecast projects l's metric over the rest of its window. An
// equivalent-basis budget is not forecast: the daily rows carry real
// spend only, and its threshold alerts still fire. Nor is a budget scoped
// to a tag: the daily rows are all spend, not the tag's.
func (w *Watcher) forecast(ctx context.Context, l Limit, now time.Time) []forecast.Prediction {
	if l.Basis == budget.BasisEquivalent || l.Tag != "" {
		return nil
	}
	metric := forecast.CostUSD
	if l.Basis == budget.BasisTokens {
		metric = forecast.TotalTokens
	}
	start := budget.WindowStart(l.Window, now)
	daysLeft := int(budget.WindowEnd(l.Window, start).Sub(now).Hours()/24) + 1
	rows, err := w.Source.AggregateBy(ctx, analytics.Filter{
		Since: now.Add(-forecastHistory), WorkflowID: l.WorkflowID, AgentID: l.AgentID,
	}, analytics.BucketDay, analytics.GroupNone)
	if err != nil {
		return nil
	}
	return forecast.AutoForecast(forecast.SeriesFromRows(rows, metric), daysLeft, 24*time.Hour)
}

// publishExceeded raises budget.exceeded for a once per window: not when
// the budget is short of its limit, not again on a later tick, and not
// again after a restart when the audit log already has it.
func (w *Watcher) publishExceeded(ctx context.Context, a Alert, windowStart, now time.Time) {
	if w.Publish == nil {
		return
	}
	env, ok := budget.ExceededEvent(a, now)
	if !ok {
		return
	}
	key := "exceeded|" + a.Limit.Key() + "|" + windowStart.Format(time.RFC3339)
	if w.seen[key] {
		return
	}
	w.seen[key] = true
	if w.Recorded != nil && w.Recorded(ctx, a.Limit.Key(), windowStart) {
		return
	}
	w.Publish.Publish(env)
}

// RecordedIn reads whether a budget was recorded as exceeded from the
// audit log in store, for Watcher.Recorded. A failed read counts as not
// recorded: a duplicate entry is the lesser harm than a lost one.
func RecordedIn(store *sqlite.Store) func(ctx context.Context, name string, since time.Time) bool {
	return func(ctx context.Context, name string, since time.Time) bool {
		log, err := auditlog.Read(ctx, store, auditlog.Query{Action: string(audit.ActionBudgetExceeded), Since: since})
		if err != nil {
			return false
		}
		for _, e := range log.Entries {
			if e.Target == name {
				return true
			}
		}
		return false
	}
}
