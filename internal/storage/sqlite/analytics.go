package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// *Store is the analytics aggregator's read side: it implements
// analytics.Store over the events table. The sums and groupings are SQL;
// pricing them, and accounting for what could not be priced, is the
// aggregator's.
var _ analytics.Store = (*Store)(nil)

// Stored events carry the cache portions of their input in the payload.
// Claude Code events written before the payload had cache-write fields
// recorded the writes as the cache_creation_input attribute, inside an
// InputTokens that already included them, so they are read as writes too.
// No other source recorded writes inside InputTokens before the payload
// field existed, so no other attribute is a safe fallback.
//
// These expressions, and the cost-source ones below, are indexed verbatim
// by events_usage_idx (migration 5) so the rollups never read the table.
// Change one and the index stops covering it: change both together.
const (
	cacheReadExpr    = `CAST(COALESCE(json_extract(payload, '$.cached_input_tokens'), json_extract(attributes, '$.cache_read_input')) AS INTEGER)`
	cacheWriteExpr   = `CAST(COALESCE(json_extract(payload, '$.cache_write_input_tokens'), json_extract(attributes, '$.cache_creation_input')) AS INTEGER)`
	cacheWrite1hExpr = `CAST(json_extract(payload, '$.cache_write_1h_input_tokens') AS INTEGER)`
)

// The daily pricing groups are priced at their latest event: a day is the
// finest grain a rate card changes at in practice.
const (
	dayBucketExpr  = `timestamp_ns / 86400000000000`
	pricedAtColumn = `MAX(timestamp_ns)`
)

// costSourceMetered keeps cost RECOMPUTE away from flat-rate traffic:
// plan-included and trial events are zero-cost BY DESIGN (the request is
// covered by a subscription or vendor credit), so repricing them at list
// rates would invent spend. Note this governs repricing only — pricing
// GAPS in plan-covered traffic are reported separately, because on a
// subscription that traffic is the majority and an unpriced model there
// would otherwise leave no trace at all. The schema column carries only
// the bundled counters, so the source is read from payload JSON.
const costSourceMetered = `COALESCE(json_extract(payload, '$.cost_source'), '') NOT IN ('plan_included', 'trial')`

// costSourcePlanCovered selects the plan-included and trial traffic.
const costSourcePlanCovered = `COALESCE(json_extract(payload, '$.cost_source'), '') IN ('plan_included', 'trial')`

// usageOnly keeps prompt events that carry usage. Plan-window readings
// (the claude.ai usage meter, account readers) are stored as prompt events
// with no model and no tokens; counted, they inflated requests and were
// listed as an unpriced model — 2,179 of a month's "requests" here were
// readings. Usage with tokens but no model still counts, and shows as
// unpriced, because that is a real gap.
const usageOnly = `(COALESCE(total_tokens, 0) > 0 OR COALESCE(input_tokens, 0) > 0
		OR COALESCE(output_tokens, 0) > 0 OR COALESCE(model, '') <> '')`

// groupColumn resolves an analytics.Group to the events-table column it
// maps to, "" for none.
func groupColumn(g analytics.Group) string {
	switch g {
	case analytics.GroupProvider:
		return "provider"
	case analytics.GroupModel:
		return "model"
	case analytics.GroupWorkflow:
		return "workflow_id"
	case analytics.GroupAgent:
		return "agent_id"
	default:
		return ""
	}
}

// groupKeyExpr is the SELECT expression for a group's key.
func groupKeyExpr(g analytics.Group) string {
	if col := groupColumn(g); col != "" {
		return fmt.Sprintf("COALESCE(%s, '')", col)
	}
	return "''"
}

// bucketExpr floor-divides timestamp_ns into width-second buckets. SQLite
// has no native time bucketing, but timestamp_ns is already a monotonic
// int; converting ns to seconds first keeps the numbers small.
func bucketExpr(widthSec int64) string {
	return fmt.Sprintf("(timestamp_ns / 1000000000 / %d) * %d", widthSec, widthSec)
}

// costSelection is the WHERE conditions for a pricing query's selection.
func costSelection(sel analytics.CostSelection) []string {
	if sel == analytics.PlanCovered {
		return []string{costSourcePlanCovered}
	}
	return []string{"(cost_usd IS NULL OR cost_usd = 0)", costSourceMetered}
}

// UsageBuckets implements analytics.Store.
func (s *Store) UsageBuckets(ctx context.Context, f analytics.Filter, widthSec int64, group analytics.Group) ([]analytics.Row, error) {
	if s == nil {
		return nil, analytics.ErrNotInitialised
	}
	q, args := usageBucketsQuery(f, widthSec, group)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []analytics.Row
	for rows.Next() {
		var (
			bucketStartSec int64
			groupKey       string
			r              analytics.Row
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
	return out, nil
}

// usageBucketsQuery is UsageBuckets' statement.
func usageBucketsQuery(f analytics.Filter, widthSec int64, group analytics.Group) (string, []any) {
	conds, args := analyticsConditions(f)
	groupCol := groupColumn(group)
	selectCols := []string{
		bucketExpr(widthSec) + " AS bucket_start_sec",
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
	return q, args
}

// BucketPricingGroups implements analytics.Store. It groups on the same
// bucket and key expressions UsageBuckets does, so its groups land on
// UsageBuckets' rows exactly.
func (s *Store) BucketPricingGroups(ctx context.Context, f analytics.Filter, widthSec int64, group analytics.Group, sel analytics.CostSelection) ([]analytics.PricingGroup, error) {
	if s == nil {
		return nil, analytics.ErrNotInitialised
	}
	what := "recompute"
	if sel == analytics.PlanCovered {
		what = "plan-covered group value"
	}
	q, args := bucketPricingQuery(f, widthSec, group, sel)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: %s query: %w", what, err)
	}
	defer func() { _ = rows.Close() }()

	var out []analytics.PricingGroup
	for rows.Next() {
		var (
			bucketSec                                        int64
			g                                                analytics.PricingGroup
			provider, model                                  sql.NullString
			events, inTok, outTok, cacheIn, cacheW, cacheW1h sql.NullInt64
		)
		if err := rows.Scan(&bucketSec, &g.GroupKey, &provider, &model, &events, &inTok, &outTok, &cacheIn, &cacheW, &cacheW1h); err != nil {
			return nil, fmt.Errorf("analytics: %s scan: %w", what, err)
		}
		g.At = time.Unix(bucketSec, 0).UTC()
		g.Provider, g.Model = provider.String, model.String
		g.Events, g.InputTokens, g.OutputTokens = events.Int64, inTok.Int64, outTok.Int64
		g.CacheReadTokens, g.CacheWriteTokens, g.CacheWrite1hTokens = cacheIn.Int64, cacheW.Int64, cacheW1h.Int64
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics: %s iterate: %w", what, err)
	}
	return out, nil
}

// bucketPricingQuery is BucketPricingGroups' statement.
func bucketPricingQuery(f analytics.Filter, widthSec int64, group analytics.Group, sel analytics.CostSelection) (string, []any) {
	conds, args := analyticsConditions(f)
	conds = append(conds, costSelection(sel)...)
	q := "SELECT " + bucketExpr(widthSec) + " AS bucket_start_sec, " + groupKeyExpr(group) + " AS group_key," +
		` provider, model, COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(` + cacheReadExpr + `), 0),
			COALESCE(SUM(` + cacheWriteExpr + `), 0),
			COALESCE(SUM(` + cacheWrite1hExpr + `), 0)
		FROM events WHERE ` + strings.Join(conds, " AND ") +
		" GROUP BY bucket_start_sec, group_key, provider, model"
	return q, args
}

// UsageTotals implements analytics.Store.
func (s *Store) UsageTotals(ctx context.Context, f analytics.Filter) (analytics.Summary, error) {
	if s == nil {
		return analytics.Summary{}, analytics.ErrNotInitialised
	}
	q, args := usageTotalsQuery(f)
	var sum analytics.Summary
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(
		&sum.Requests, &sum.InputTokens, &sum.OutputTokens, &sum.TotalTokens, &sum.CostUSD,
	); err != nil {
		return analytics.Summary{}, fmt.Errorf("analytics: summarize: %w", err)
	}
	return sum, nil
}

// usageTotalsQuery is UsageTotals' statement.
func usageTotalsQuery(f analytics.Filter) (string, []any) {
	conds, args := analyticsConditions(f)
	q := `SELECT COUNT(*),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(cost_usd), 0)
		FROM events`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	return q, args
}

// DailyPricingGroups implements analytics.Store. The uncosted-metered
// groups come back ordered by provider and model; the plan-covered ones in
// SQLite's grouping order.
func (s *Store) DailyPricingGroups(ctx context.Context, f analytics.Filter, sel analytics.CostSelection) ([]analytics.PricingGroup, error) {
	if s == nil {
		return nil, analytics.ErrNotInitialised
	}
	what := "summarize recompute"
	if sel == analytics.PlanCovered {
		what = "plan-covered value"
	}
	q, args := dailyPricingQuery(f, sel)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: %s query: %w", what, err)
	}
	defer func() { _ = rows.Close() }()

	var out []analytics.PricingGroup
	for rows.Next() {
		var (
			pricedAtNs                                         int64
			g                                                  analytics.PricingGroup
			provider, model                                    sql.NullString
			requests, inTok, outTok, cacheIn, cacheW, cacheW1h sql.NullInt64
		)
		if err := rows.Scan(&pricedAtNs, &provider, &model, &requests, &inTok, &outTok, &cacheIn, &cacheW, &cacheW1h); err != nil {
			return nil, fmt.Errorf("analytics: %s scan: %w", what, err)
		}
		g.At = time.Unix(0, pricedAtNs).UTC()
		g.Provider, g.Model = provider.String, model.String
		g.Events, g.InputTokens, g.OutputTokens = requests.Int64, inTok.Int64, outTok.Int64
		g.CacheReadTokens, g.CacheWriteTokens, g.CacheWrite1hTokens = cacheIn.Int64, cacheW.Int64, cacheW1h.Int64
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics: %s iterate: %w", what, err)
	}
	return out, nil
}

// dailyPricingQuery is DailyPricingGroups' statement.
func dailyPricingQuery(f analytics.Filter, sel analytics.CostSelection) (string, []any) {
	order := ` ORDER BY provider, model`
	if sel == analytics.PlanCovered {
		order = ""
	}
	conds, args := analyticsConditions(f)
	conds = append(conds, costSelection(sel)...)
	q := `SELECT ` + pricedAtColumn + `, provider, model, COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(` + cacheReadExpr + `), 0),
			COALESCE(SUM(` + cacheWriteExpr + `), 0),
			COALESCE(SUM(` + cacheWrite1hExpr + `), 0)
		FROM events WHERE ` + strings.Join(conds, " AND ") +
		` GROUP BY ` + dayBucketExpr + `, provider, model` + order
	return q, args
}

// CacheTotals implements analytics.Store. JSONL events carry the cache
// split in payload.cached_input_tokens (post-v0.14.2 poller) or
// attributes.cache_read_input (legacy); COALESCE-over-both so old events
// still pay the cache discount without a re-ingest.
func (s *Store) CacheTotals(ctx context.Context, f analytics.Filter) (analytics.CacheTotals, error) {
	if s == nil {
		return analytics.CacheTotals{}, analytics.ErrNotInitialised
	}
	conds, args := analyticsConditions(f)
	q := `SELECT
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(CAST(COALESCE(json_extract(payload, '$.cached_input_tokens'), json_extract(attributes, '$.cache_read_input')) AS INTEGER)), 0),
		COALESCE(SUM(output_tokens), 0)
		FROM events`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	var t analytics.CacheTotals
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(
		&t.InputTokens, &t.CachedInputTokens, &t.OutputTokens,
	); err != nil {
		return analytics.CacheTotals{}, fmt.Errorf("analytics: cache_stats: %w", err)
	}
	return t, nil
}

// SessionUsage implements analytics.Store.
func (s *Store) SessionUsage(ctx context.Context, f analytics.Filter) ([]analytics.SessionUsage, error) {
	if s == nil {
		return nil, analytics.ErrNotInitialised
	}
	conds, args := analyticsConditions(f)
	conds = append(conds, `COALESCE(session_id, '') <> ''`)
	q := `SELECT session_id, timestamp_ns, COALESCE(provider, ''), COALESCE(model, ''),
			COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(total_tokens, 0),
			COALESCE(` + cacheReadExpr + `, 0),
			COALESCE(` + cacheWriteExpr + `, 0),
			COALESCE(` + cacheWrite1hExpr + `, 0),
			COALESCE(cost_usd, 0)
		FROM events WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY timestamp_ns`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: session turns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []analytics.SessionUsage
	for rows.Next() {
		var (
			u    analytics.SessionUsage
			ns   int64
			cost sql.NullFloat64
		)
		if err := rows.Scan(&u.SessionID, &ns, &u.Provider, &u.Model, &u.InputTokens, &u.OutputTokens, &u.TotalTokens,
			&u.CacheReadTokens, &u.CacheWriteTokens, &u.CacheWrite1hTokens, &cost); err != nil {
			return nil, fmt.Errorf("analytics: session turns scan: %w", err)
		}
		u.At = time.Unix(0, ns).UTC()
		u.CostUSD = cost.Float64
		out = append(out, u)
	}
	return out, rows.Err()
}

// analyticsConditions translates an analytics.Filter into WHERE conditions
// and their arguments.
func analyticsConditions(f analytics.Filter) ([]string, []any) {
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
	if f.EventType == "" || f.EventType == eventschema.EventTypePrompt {
		conds = append(conds, usageOnly)
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
	if excludes := f.ExcludedSources(); len(excludes) > 0 {
		placeholders := make([]string, len(excludes))
		for i, src := range excludes {
			placeholders[i] = "?"
			args = append(args, src)
		}
		conds = append(conds, "(source IS NULL OR source NOT IN ("+strings.Join(placeholders, ", ")+"))")
	}
	return conds, args
}
