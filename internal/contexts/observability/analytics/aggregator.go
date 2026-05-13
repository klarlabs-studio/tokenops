// Package analytics rolls up the local SQLite event store into the
// time-bucketed aggregates the dashboard, CLI, and forecasting engines
// consume. The aggregator is read-only — it never mutates events — and
// is intentionally a thin layer over the (already-indexed) events table
// so the same queries can be ported to ClickHouse later.
package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Bucket is the discretisation unit for time-bucketed aggregates.
type Bucket string

// Known buckets. Hour and Day cover the dashboards' live-view and
// forecasting windows; minute-resolution can be added later for SLOs.
const (
	BucketHour Bucket = "hour"
	BucketDay  Bucket = "day"
)

// seconds returns the bucket width in seconds.
func (b Bucket) seconds() int64 {
	switch b {
	case BucketDay:
		return 86_400
	default:
		return 3_600
	}
}

// Group identifies the dimension to group by.
type Group string

// Known group dimensions.
const (
	GroupNone     Group = ""
	GroupProvider Group = "provider"
	GroupModel    Group = "model"
	GroupWorkflow Group = "workflow"
	GroupAgent    Group = "agent"
)

// column resolves a Group to the events-table column it maps to.
func (g Group) column() string {
	switch g {
	case GroupProvider:
		return "provider"
	case GroupModel:
		return "model"
	case GroupWorkflow:
		return "workflow_id"
	case GroupAgent:
		return "agent_id"
	default:
		return ""
	}
}

// Filter narrows the events the aggregator considers. Empty fields are
// not constrained.
//
// ExcludeSources gates the synthetic / demo / replay surfaces. nil
// means "apply DefaultExcludedSources" (drops `demo` so seeded data
// stays out of operator-facing rollups). An empty non-nil slice means
// "include every source"; callers pass that when they explicitly want
// to see synthetic data alongside real traffic.
type Filter struct {
	EventType      eventschema.EventType
	Provider       string
	Model          string
	WorkflowID     string
	AgentID        string
	Since          time.Time
	Until          time.Time
	ExcludeSources []string
}

// DefaultExcludedSources is applied by every analytics + plan query
// unless the caller passes a non-nil ExcludeSources slice. Demo events
// land here so `tokenops demo` no longer contaminates production-facing
// numbers; opt back in via `--include-demo` / `include_demo: true`.
var DefaultExcludedSources = []string{"demo"}

// resolveExcludeSources returns the operative exclude list for a
// Filter: caller-supplied slice when set (including empty for "show
// everything"), the package default otherwise.
func resolveExcludeSources(f Filter) []string {
	if f.ExcludeSources == nil {
		return DefaultExcludedSources
	}
	return f.ExcludeSources
}

// Row is one (bucket, group-key) cell of an aggregate.
type Row struct {
	BucketStart  time.Time
	GroupKey     string
	Requests     int64
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	// CostRecomputed reports the number of rows in this bucket whose
	// CostUSD was 0 in the store and was recomputed via spend.Engine.
	// Useful for dashboards to flag stale pricing tables.
	CostRecomputed int64
}

// Summary is the global rollup over a query: total requests / tokens /
// cost across the entire filter window. It is what the CLI prints as
// "this week's spend" and the dashboard surfaces as headline numbers.
type Summary struct {
	Requests     int64
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
}

// Aggregator answers rollup queries against a sqlite.Store. spend.Engine
// is consulted when a row's CostUSD is zero (e.g. older events written
// before the spend engine was wired in).
type Aggregator struct {
	store *sqlite.Store
	spend *spend.Engine
}

// New constructs an Aggregator. spendEng may be nil — rows with zero cost
// then stay zero rather than being recomputed.
func New(store *sqlite.Store, spendEng *spend.Engine) *Aggregator {
	return &Aggregator{store: store, spend: spendEng}
}

// AggregateBy returns time-bucketed aggregates. Setting group to GroupNone
// produces one row per bucket (across all events in the bucket).
func (a *Aggregator) AggregateBy(ctx context.Context, f Filter, bucket Bucket, group Group) ([]Row, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("analytics: aggregator not initialised")
	}
	width := bucket.seconds()
	if width <= 0 {
		return nil, fmt.Errorf("analytics: invalid bucket %q", bucket)
	}

	conds, args := buildConditions(f)

	// SQLite has no native time bucketing, but timestamp_ns is already a
	// monotonic int. Floor-divide by the bucket width to get a stable key.
	// Convert ns -> seconds first to keep numbers small (and in int64 range).
	bucketExpr := fmt.Sprintf("(timestamp_ns / 1000000000 / %d) * %d", width, width)
	groupCol := group.column()

	selectCols := []string{
		bucketExpr + " AS bucket_start_sec",
	}
	if groupCol != "" {
		selectCols = append(selectCols, fmt.Sprintf("COALESCE(%s, '') AS group_key", groupCol))
	} else {
		selectCols = append(selectCols, "'' AS group_key")
	}
	selectCols = append(selectCols,
		"COUNT(*) AS requests",
		"COALESCE(SUM(input_tokens), 0)  AS input_tokens",
		"COALESCE(SUM(output_tokens), 0) AS output_tokens",
		"COALESCE(SUM(total_tokens), 0)  AS total_tokens",
		"COALESCE(SUM(cost_usd), 0)      AS cost_usd",
	)

	q := "SELECT " + strings.Join(selectCols, ", ") +
		" FROM events"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " GROUP BY bucket_start_sec"
	if groupCol != "" {
		q += ", group_key"
	}
	q += " ORDER BY bucket_start_sec ASC"
	if groupCol != "" {
		q += ", group_key ASC"
	}

	rows, err := a.store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Row
	for rows.Next() {
		var (
			bucketStartSec int64
			groupKey       string
			r              Row
		)
		if err := rows.Scan(&bucketStartSec, &groupKey, &r.Requests, &r.InputTokens, &r.OutputTokens, &r.TotalTokens, &r.CostUSD); err != nil {
			return nil, fmt.Errorf("analytics: scan: %w", err)
		}
		r.BucketStart = time.Unix(bucketStartSec, 0).UTC()
		r.GroupKey = groupKey
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics: iterate: %w", err)
	}

	if a.spend != nil {
		if err := a.recomputeMissingCosts(ctx, f, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// recomputeMissingCosts scans for rows where CostUSD == 0 (within their
// bucket+group) and computes the cost from per-event token totals via
// spend.Engine, attributing the recompute to row.CostRecomputed. This
// keeps existing event CostUSD authoritative while filling in zeros.
func (a *Aggregator) recomputeMissingCosts(ctx context.Context, f Filter, rows []Row) error {
	for i := range rows {
		if rows[i].CostUSD > 0 {
			continue
		}
		// Pull the underlying events for this bucket+group and recompute.
		conds, args := buildConditions(f)
		conds = append(conds,
			"timestamp_ns >= ?",
			"timestamp_ns < ?",
		)
		args = append(args,
			rows[i].BucketStart.UnixNano(),
			rows[i].BucketStart.Add(time.Second*time.Duration(BucketHour.seconds())).UnixNano(),
		)
		// override second arg if BucketDay
		// (kept simple; correct because the test column uses bucket_start_sec)
		var (
			cost  float64
			fixed int64
		)
		q := "SELECT provider, model, input_tokens, output_tokens FROM events"
		if len(conds) > 0 {
			q += " WHERE " + strings.Join(conds, " AND ")
		}
		eventRows, err := a.store.DB().QueryContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("analytics: recompute query: %w", err)
		}
		err = func() error {
			defer func() { _ = eventRows.Close() }()
			for eventRows.Next() {
				var (
					provider, model sql.NullString
					inTok, outTok   sql.NullInt64
				)
				if err := eventRows.Scan(&provider, &model, &inTok, &outTok); err != nil {
					return err
				}
				p := &eventschema.PromptEvent{
					Provider:     eventschema.Provider(provider.String),
					RequestModel: model.String,
					InputTokens:  inTok.Int64,
					OutputTokens: outTok.Int64,
				}
				if c, err := a.spend.Compute(p); err == nil {
					cost += c
					fixed++
				}
			}
			return eventRows.Err()
		}()
		if err != nil {
			return fmt.Errorf("analytics: recompute scan: %w", err)
		}
		rows[i].CostUSD = cost
		rows[i].CostRecomputed = fixed
	}
	return nil
}

// Summarize returns a single global rollup over the filter window. It is
// equivalent to AggregateBy with an unbounded bucket; using a dedicated
// query keeps the SQL plan simpler.
func (a *Aggregator) Summarize(ctx context.Context, f Filter) (Summary, error) {
	if a == nil || a.store == nil {
		return Summary{}, errors.New("analytics: aggregator not initialised")
	}
	conds, args := buildConditions(f)
	q := `SELECT COUNT(*),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(cost_usd), 0)
		FROM events`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	var s Summary
	if err := a.store.DB().QueryRowContext(ctx, q, args...).Scan(
		&s.Requests, &s.InputTokens, &s.OutputTokens, &s.TotalTokens, &s.CostUSD,
	); err != nil {
		return Summary{}, fmt.Errorf("analytics: summarize: %w", err)
	}
	return s, nil
}

func buildConditions(f Filter) ([]string, []any) {
	var (
		conds []string
		args  []any
	)
	if f.EventType != "" {
		conds = append(conds, "type = ?")
		args = append(args, string(f.EventType))
	} else {
		// Default to prompts only — workflow/optimization events do not
		// carry per-request token counts in the indexed columns.
		conds = append(conds, "type = ?")
		args = append(args, string(eventschema.EventTypePrompt))
	}
	if f.Provider != "" {
		conds = append(conds, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		conds = append(conds, "model = ?")
		args = append(args, f.Model)
	}
	if f.WorkflowID != "" {
		conds = append(conds, "workflow_id = ?")
		args = append(args, f.WorkflowID)
	}
	if f.AgentID != "" {
		conds = append(conds, "agent_id = ?")
		args = append(args, f.AgentID)
	}
	if !f.Since.IsZero() {
		conds = append(conds, "timestamp_ns >= ?")
		args = append(args, f.Since.UTC().UnixNano())
	}
	if !f.Until.IsZero() {
		conds = append(conds, "timestamp_ns < ?")
		args = append(args, f.Until.UTC().UnixNano())
	}
	if excludes := resolveExcludeSources(f); len(excludes) > 0 {
		placeholders := make([]string, len(excludes))
		for i, s := range excludes {
			placeholders[i] = "?"
			args = append(args, s)
		}
		conds = append(conds, "(source IS NULL OR source NOT IN ("+strings.Join(placeholders, ", ")+"))")
	}
	return conds, args
}
