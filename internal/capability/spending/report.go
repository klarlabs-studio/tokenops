package spending

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// The analytics vocabulary the surfaces render, beside EventAggregator
// and Window in rollups.go. Aliases, so a payload marshals exactly as the
// domain type does and the adapters do not reach into the domain to name
// it.
type (
	// Row is one (bucket, group) cell of a rollup.
	Row = analytics.Row
	// Totals is the headline summary of a window.
	Totals = analytics.Summary
	// Group is what rows are grouped by.
	Group = analytics.Group
)

// NewAggregator rolls up store's events, priced by eng.
func NewAggregator(store *sqlite.Store, eng *spend.Engine) *EventAggregator {
	return analytics.New(store, eng)
}

// GroupOf is the grouping a caller named: model, provider, workflow or
// agent, model when by is empty. The match is exact; callers normalise.
// It is the one table of groupings every surface accepts.
func GroupOf(by string) (Group, bool) {
	if by == "" {
		return analytics.GroupModel, true
	}
	g, ok := groups[by]
	return g, ok
}

// ExcludedByDefault is the sources every query drops unless re-admitted.
func ExcludedByDefault() []string {
	return slices.Clone(analytics.DefaultExcludedSources)
}

// IsExcludedByDefault reports whether source is dropped unless re-admitted.
func IsExcludedByDefault(source string) bool {
	return slices.Contains(analytics.DefaultExcludedSources, source)
}

// RollUp folds per-bucket rows into one row per group key, ranked on the
// API equivalent, then tokens, then the key, and keeps the first n (all
// when n is not positive). Each folded row keeps the first bucket's
// provenance; the counts and dollar figures are summed.
//
// Ranked on the API equivalent rather than the real cost: on a flat-rate
// plan every row's cost is $0 by design, so a cost-keyed ranking put
// every consumer in a tie and ranked nothing.
func RollUp(rows []Row, n int) []Row {
	if len(rows) == 0 {
		return nil
	}
	totals := make(map[string]*Row)
	for i := range rows {
		r := rows[i]
		if cur, ok := totals[r.GroupKey]; ok {
			cur.Requests += r.Requests
			cur.InputTokens += r.InputTokens
			cur.OutputTokens += r.OutputTokens
			cur.TotalTokens += r.TotalTokens
			cur.CostUSD += r.CostUSD
			cur.APIEquivalentUSD += r.APIEquivalentUSD
			continue
		}
		totals[r.GroupKey] = &r
	}
	out := make([]Row, 0, len(totals))
	for _, r := range totals {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.APIEquivalentUSD != b.APIEquivalentUSD {
			return a.APIEquivalentUSD > b.APIEquivalentUSD
		}
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		return a.GroupKey < b.GroupKey
	})
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out
}

// Source is what a spend report reads.
type Source interface {
	Aggregator
	Summarize(ctx context.Context, f Window) (Totals, error)
}

// ReportQuery asks for a window's spend report.
type ReportQuery struct {
	Filter Window
	Group  Group
	// Top is how many consumers to keep; all when not positive.
	Top int
	// Forecast asks for a projection Horizon days ahead (seven when not
	// positive).
	Forecast bool
	Horizon  int
}

// Report is a window's spend: the headline, the top consumers, the
// last day's burn and, when asked, the projection.
type Report struct {
	Totals Totals
	// Top is the window's consumers, rolled up and ranked.
	Top []Row
	// Burn is the last 24 hours, hour by hour, whatever the window.
	Burn       []Row
	BurnCost   float64
	BurnTokens int64
	// Forecast and ForecastTokens project the window's daily rows.
	Forecast, ForecastTokens []Prediction
}

// burnWindow is the burn rate's fixed look-back.
const burnWindow = 24 * time.Hour

// SpendReport answers `tokenops spend`: the window's totals and top
// consumers, the last day's burn and an optional projection.
func SpendReport(ctx context.Context, src Source, q ReportQuery, now time.Time) (Report, error) {
	totals, err := src.Summarize(ctx, q.Filter)
	if err != nil {
		return Report{}, err
	}
	rows, err := src.AggregateBy(ctx, q.Filter, analytics.BucketDay, q.Group)
	if err != nil {
		return Report{}, err
	}
	// The burn window ignores the report's source filter: it is the
	// machine's last day, as it always has been.
	burn, err := src.AggregateBy(ctx, Window{Since: now.Add(-burnWindow)}, analytics.BucketHour, analytics.GroupNone)
	if err != nil {
		return Report{}, err
	}
	out := Report{Totals: totals, Top: RollUp(rows, q.Top), Burn: burn}
	for _, r := range burn {
		out.BurnCost += r.CostUSD
		out.BurnTokens += r.TotalTokens
	}
	if q.Forecast {
		out.Forecast, out.ForecastTokens = Project(rows, q.Horizon)
	}
	return out, nil
}

// WindowLabel describes a window's bounds, "all time" when it has none.
func WindowLabel(f Window) string {
	parts := make([]string, 0, 2)
	if !f.Since.IsZero() {
		parts = append(parts, "since="+f.Since.Format(time.RFC3339))
	}
	if !f.Until.IsZero() {
		parts = append(parts, "until="+f.Until.Format(time.RFC3339))
	}
	if len(parts) == 0 {
		return "all time"
	}
	return strings.Join(parts, " ")
}
