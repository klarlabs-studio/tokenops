package spending

import (
	"context"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The rollups below are the daemon's original spend read models —
// summary, series, forecast, cache stats, per-workflow totals and the
// optimizer's decisions — served on /api/spend/*, /api/workflows and
// /api/optimizations (ADR 0010). Their answers keep the field names and
// order those routes have always sent.

// EventAggregator is the store-backed aggregator every rollup reads.
// Aliased so analytics stays its single definition, while adapters name
// the capability's type and do not reach past it.
type EventAggregator = analytics.Aggregator

// Window is the slice of stored events a rollup covers.
type Window = analytics.Filter

// WindowQuery is a window as a caller wrote it: since is RFC 3339 or a
// duration such as 24h, until is RFC 3339, the rest narrow by exact
// match.
type WindowQuery struct {
	Since, Until string
	Provider     string
	Model        string
	WorkflowID   string
	AgentID      string
}

// WindowOf parses q, starting defaultSince before now when it names no
// since. Its error is the caller's mistake and its text is fit to show
// them.
func WindowOf(q WindowQuery, defaultSince time.Duration) (Window, error) {
	return analytics.QueryParams{
		Since:        q.Since,
		Until:        q.Until,
		Provider:     q.Provider,
		Model:        q.Model,
		WorkflowID:   q.WorkflowID,
		AgentID:      q.AgentID,
		DefaultSince: defaultSince,
	}.ToFilter()
}

// SummaryReport is spend and tokens over a window.
type SummaryReport struct {
	Currency string            `json:"currency"`
	Summary  analytics.Summary `json:"summary"`
	Window   Window            `json:"window"`
}

// Summary totals the window.
func Summary(ctx context.Context, s Summarizer, w Window, currency string) (SummaryReport, error) {
	sum, err := s.Summarize(ctx, w)
	if err != nil {
		return SummaryReport{}, err
	}
	return SummaryReport{Currency: currency, Summary: sum, Window: w}, nil
}

// SeriesReport is spend and tokens bucketed over time.
type SeriesReport struct {
	Bucket   analytics.Bucket `json:"bucket"`
	Currency string           `json:"currency"`
	Group    analytics.Group  `json:"group"`
	Rows     []analytics.Row  `json:"rows"`
}

// Series buckets the window by hour, or by day when bucket says so, and
// groups it by provider, workflow, agent or model when group names one.
// Both are case-insensitive; anything else takes the default.
func Series(ctx context.Context, agg Aggregator, w Window, bucket, group string, currency string) (SeriesReport, error) {
	b, g := bucketOf(bucket), groupOf(group)
	rows, err := agg.AggregateBy(ctx, w, b, g)
	if err != nil {
		return SeriesReport{}, err
	}
	return SeriesReport{Bucket: b, Currency: currency, Group: g, Rows: rows}, nil
}

func bucketOf(s string) analytics.Bucket {
	if strings.ToLower(s) == "day" {
		return analytics.BucketDay
	}
	return analytics.BucketHour
}

func groupOf(s string) analytics.Group {
	switch strings.ToLower(s) {
	case "provider":
		return analytics.GroupProvider
	case "workflow":
		return analytics.GroupWorkflow
	case "agent":
		return analytics.GroupAgent
	case "model":
		return analytics.GroupModel
	default:
		return analytics.GroupNone
	}
}

// Forecast horizon bounds, in days.
const (
	defaultHorizonDays = 7
	maxHorizonDays     = 30
	forecastLookback   = 30 * 24 * time.Hour
)

// HorizonDays reads a requested horizon: 1 to 30 days, seven otherwise.
func HorizonDays(s string) int {
	if s == "" {
		return defaultHorizonDays
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= maxHorizonDays {
		return n
	}
	return defaultHorizonDays
}

// ForecastReport projects daily spend and tokens from the last thirty
// days.
type ForecastReport struct {
	Currency string                `json:"currency"`
	Forecast []forecast.Prediction `json:"forecast"`
	// ForecastTokens projects tokens over the same horizon. A flat-rate
	// plan bills nothing, so the cost series is a flat zero and the token
	// series is the only one a chart can plot meaningfully.
	ForecastTokens []forecast.Prediction `json:"forecast_tokens"`
	History        []analytics.Row       `json:"history"`
	HistoryPoints  int                   `json:"history_points"`
	HorizonDays    int                   `json:"horizon_days"`
}

// Forecast projects horizonDays ahead from the thirty days before now.
func Forecast(ctx context.Context, agg Aggregator, horizonDays int, currency string, now time.Time) (ForecastReport, error) {
	rows, err := agg.AggregateBy(ctx, Window{Since: now.Add(-forecastLookback)}, analytics.BucketDay, analytics.GroupNone)
	if err != nil {
		return ForecastReport{}, err
	}
	history := forecast.SeriesFromRows(rows, forecast.CostUSD)
	tokenHistory := forecast.SeriesFromRows(rows, forecast.TotalTokens)
	return ForecastReport{
		Currency:       currency,
		Forecast:       forecast.AutoForecast(history, horizonDays, 24*time.Hour),
		ForecastTokens: forecast.AutoForecast(tokenHistory, horizonDays, 24*time.Hour),
		History:        rows,
		HistoryPoints:  len(history),
		HorizonDays:    horizonDays,
	}, nil
}

// CacheStatter reports prompt-cache use in a window.
type CacheStatter interface {
	CacheStats(ctx context.Context, f analytics.Filter) (analytics.CacheStatsResult, error)
}

// CacheReport is the prompt-cache hit ratio over a window. Cache reads
// bill at about a tenth of the new-input rate for Claude models, so for
// agent-heavy work the ratio is the key efficiency number.
type CacheReport struct {
	Stats  analytics.CacheStatsResult `json:"stats"`
	Window Window                     `json:"window"`
}

// CacheStats reports the window's cache use.
func CacheStats(ctx context.Context, c CacheStatter, w Window) (CacheReport, error) {
	stats, err := c.CacheStats(ctx, w)
	if err != nil {
		return CacheReport{}, err
	}
	return CacheReport{Stats: stats, Window: w}, nil
}

// WorkflowTotal is one workflow's usage over a window.
type WorkflowTotal struct {
	WorkflowID string  `json:"workflow_id"`
	Requests   int64   `json:"requests"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
}

// WorkflowTotals is one row per workflow. The order is unspecified.
type WorkflowTotals struct {
	Currency  string          `json:"currency"`
	Workflows []WorkflowTotal `json:"workflows"`
}

// Workflows rolls the window up per workflow. Events without a workflow
// are left out.
func Workflows(ctx context.Context, agg Aggregator, w Window, currency string) (WorkflowTotals, error) {
	rows, err := agg.AggregateBy(ctx, w, analytics.BucketDay, analytics.GroupWorkflow)
	if err != nil {
		return WorkflowTotals{}, err
	}
	totals := map[string]*WorkflowTotal{}
	for _, row := range rows {
		key := row.GroupKey
		if key == "" {
			continue
		}
		cur, ok := totals[key]
		if !ok {
			cur = &WorkflowTotal{WorkflowID: key}
			totals[key] = cur
		}
		cur.Requests += row.Requests
		cur.Tokens += row.TotalTokens
		cur.CostUSD += row.CostUSD
	}
	out := make([]WorkflowTotal, 0, len(totals))
	for _, e := range totals {
		out = append(out, *e)
	}
	return WorkflowTotals{Currency: currency, Workflows: out}, nil
}

// EventQuerier reads stored events.
type EventQuerier interface {
	Query(ctx context.Context, f sqlite.Filter) ([]*eventschema.Envelope, error)
}

// maxOptimizations caps how many optimizer decisions one answer lists.
const maxOptimizations = 500

// Optimization is one decision the optimizer recorded.
type Optimization struct {
	Timestamp              time.Time `json:"timestamp"`
	Kind                   string    `json:"kind"`
	Mode                   string    `json:"mode"`
	Decision               string    `json:"decision"`
	EstimatedSavingsTokens int64     `json:"estimated_savings_tokens"`
	EstimatedSavingsUSD    float64   `json:"estimated_savings_usd"`
	QualityScore           float64   `json:"quality_score"`
	Reason                 string    `json:"reason"`
	WorkflowID             string    `json:"workflow_id,omitempty"`
	AgentID                string    `json:"agent_id,omitempty"`
}

// Optimizations is the optimizer's decisions in a window.
type Optimizations struct {
	Currency      string         `json:"currency"`
	Optimizations []Optimization `json:"optimizations"`
}

// OptimizationsIn lists the optimizer's decisions in the window, narrowed
// by its workflow and agent.
func OptimizationsIn(ctx context.Context, q EventQuerier, w Window, currency string) (Optimizations, error) {
	envs, err := q.Query(ctx, sqlite.Filter{
		Type:       eventschema.EventTypeOptimization,
		WorkflowID: w.WorkflowID,
		AgentID:    w.AgentID,
		Since:      w.Since,
		Until:      w.Until,
		Limit:      maxOptimizations,
	})
	if err != nil {
		return Optimizations{}, err
	}
	out := make([]Optimization, 0, len(envs))
	for _, env := range envs {
		oe, ok := env.Payload.(*eventschema.OptimizationEvent)
		if !ok {
			continue
		}
		out = append(out, Optimization{
			Timestamp:              env.Timestamp,
			Kind:                   string(oe.Kind),
			Mode:                   string(oe.Mode),
			Decision:               string(oe.Decision),
			EstimatedSavingsTokens: oe.EstimatedSavingsTokens,
			EstimatedSavingsUSD:    oe.EstimatedSavingsUSD,
			QualityScore:           oe.QualityScore,
			Reason:                 oe.Reason,
			WorkflowID:             oe.WorkflowID,
			AgentID:                oe.AgentID,
		})
	}
	return Optimizations{Currency: currency, Optimizations: out}, nil
}
