// Package spending answers where usage went: the top consumers and the
// burn rate. The MCP tools and the daemon API both call it (ADR 0010).
package spending

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// Aggregator rolls stored events up into rows.
type Aggregator interface {
	AggregateBy(ctx context.Context, f analytics.Filter, b analytics.Bucket, g analytics.Group) ([]analytics.Row, error)
}

// groups are the groupings a caller may ask for.
var groups = map[string]analytics.Group{
	"model":    analytics.GroupModel,
	"provider": analytics.GroupProvider,
	"workflow": analytics.GroupWorkflow,
	"agent":    analytics.GroupAgent,
}

// TopQuery asks for the top consumers.
type TopQuery struct {
	// By is model (default), provider, workflow or agent.
	By string
	// Top is how many to return; five when not positive.
	Top int
	// Since defaults to seven days before now; Until is open when zero.
	Since, Until time.Time
	// IncludeSources widens the default sources.
	IncludeSources []string
}

// Consumer is one group's usage.
type Consumer struct {
	Key      string  `json:"key"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
	// APIEquivalentUSD is what the group would have billed at API list
	// prices. On a flat-rate plan CostUSD is $0 for every group, so this
	// is the only dollar figure that tells the groups apart, and the one
	// the list is ranked on.
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
}

// TopConsumers is the ranked answer.
type TopConsumers struct {
	// By is the grouping applied, "model" when the caller gave none.
	By       string     `json:"by"`
	Top      []Consumer `json:"top"`
	Currency string     `json:"currency"`
}

// ErrUnknownGrouping is returned for a By that is not a grouping.
var ErrUnknownGrouping = fmt.Errorf("unknown grouping (want model, provider, workflow, or agent)")

// Top ranks the consumers in the window.
func Top(ctx context.Context, agg Aggregator, q TopQuery, currency string, now time.Time) (TopConsumers, error) {
	by := strings.ToLower(strings.TrimSpace(q.By))
	if by == "" {
		by = "model"
	}
	group, ok := groups[by]
	if !ok {
		// Falling back to model answered a typo with a confident ranking
		// of something nobody asked about.
		return TopConsumers{}, fmt.Errorf("by %q: %w", q.By, ErrUnknownGrouping)
	}
	f := analytics.Filter{Since: q.Since, Until: q.Until, IncludeSources: dedupe(q.IncludeSources)}
	if f.Since.IsZero() {
		f.Since = now.Add(-7 * 24 * time.Hour)
	}
	rows, err := agg.AggregateBy(ctx, f, analytics.BucketDay, group)
	if err != nil {
		return TopConsumers{}, err
	}
	out := rank(rows)
	top := q.Top
	if top <= 0 {
		top = 5
	}
	if top < len(out) {
		out = out[:top]
	}
	return TopConsumers{By: by, Top: out, Currency: currency}, nil
}

// rank folds per-bucket rows into one entry per group, ranked on the API
// equivalent, then tokens (unpriced models are $0 on both figures), then
// the key, so the order is the same on every call.
func rank(rows []analytics.Row) []Consumer {
	byKey := map[string]*Consumer{}
	for _, r := range rows {
		c, ok := byKey[r.GroupKey]
		if !ok {
			c = &Consumer{Key: r.GroupKey}
			byKey[r.GroupKey] = c
		}
		c.Requests += r.Requests
		c.Tokens += r.TotalTokens
		c.CostUSD += r.CostUSD
		c.APIEquivalentUSD += r.APIEquivalentUSD
	}
	out := make([]Consumer, 0, len(byKey))
	for _, c := range byKey {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.APIEquivalentUSD != b.APIEquivalentUSD {
			return a.APIEquivalentUSD > b.APIEquivalentUSD
		}
		if a.Tokens != b.Tokens {
			return a.Tokens > b.Tokens
		}
		return a.Key < b.Key
	})
	return out
}

// Burn is usage over the last hours.
type Burn struct {
	Hours  int     `json:"hours"`
	Cost   float64 `json:"cost"`
	Tokens int64   `json:"tokens"`
	// APIEquivalentUSD is the list-price value of the window. On a
	// flat-rate plan cost is structurally zero, and this is the dollar
	// figure that still moves with the work.
	APIEquivalentUSD float64         `json:"api_equivalent_usd"`
	Hourly           []analytics.Row `json:"hourly"`
	Currency         string          `json:"currency"`
}

// BurnRate totals the last hours (24 when not positive), hour by hour.
func BurnRate(ctx context.Context, agg Aggregator, hours int, include []string, currency string, now time.Time) (Burn, error) {
	if hours <= 0 {
		hours = 24
	}
	f := analytics.Filter{Since: now.Add(-time.Duration(hours) * time.Hour), IncludeSources: dedupe(include)}
	rows, err := agg.AggregateBy(ctx, f, analytics.BucketHour, analytics.GroupNone)
	if err != nil {
		return Burn{}, err
	}
	b := Burn{Hours: hours, Hourly: rows, Currency: currency}
	for _, r := range rows {
		b.Cost += r.CostUSD
		b.Tokens += r.TotalTokens
		b.APIEquivalentUSD += r.APIEquivalentUSD
	}
	return b, nil
}

// dedupe trims and de-duplicates sources, nil when none remain.
func dedupe(sources []string) []string {
	seen := make(map[string]bool, len(sources))
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
