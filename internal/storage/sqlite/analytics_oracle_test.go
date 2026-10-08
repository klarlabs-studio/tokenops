package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// The spend rollups used to read a window three times: the usage sums,
// the uncosted metered pricing groups and the plan-covered ones, each its
// own GROUP BY over events_usage_idx. BucketUsage and WindowUsage now read
// it once and group in Go. These are those three statements, kept
// verbatim, as the oracle the single read must equal bit for bit.

// oracleStore answers analytics.Store the way it was answered before the
// single read: three statements per rollup.
type oracleStore struct{ *Store }

var _ analytics.Store = oracleStore{}

// oracleSelection is the old CostSelection.
type oracleSelection int

const (
	oracleUncosted oracleSelection = iota + 1
	oraclePlanCovered
)

const (
	oracleDayBucketExpr  = `timestamp_ns / 86400000000000`
	oraclePricedAtColumn = `MAX(timestamp_ns)`
	oracleMetered        = `COALESCE(json_extract(payload, '$.cost_source'), '') NOT IN ('plan_included', 'trial')`
)

func (o oracleStore) BucketUsage(ctx context.Context, f analytics.Filter, widthSec int64, group analytics.Group) (analytics.BucketUsage, error) {
	var (
		out analytics.BucketUsage
		err error
	)
	if out.Rows, err = o.usageBuckets(ctx, f, widthSec, group); err != nil {
		return out, err
	}
	if out.Uncosted, err = o.bucketPricingGroups(ctx, f, widthSec, group, oracleUncosted); err != nil {
		return out, err
	}
	out.PlanCovered, err = o.bucketPricingGroups(ctx, f, widthSec, group, oraclePlanCovered)
	return out, err
}

func (o oracleStore) WindowUsage(ctx context.Context, f analytics.Filter) (analytics.WindowUsage, error) {
	var (
		out analytics.WindowUsage
		err error
	)
	if out.Totals, err = o.usageTotals(ctx, f); err != nil {
		return out, err
	}
	if out.Uncosted, err = o.dailyPricingGroups(ctx, f, oracleUncosted); err != nil {
		return out, err
	}
	out.PlanCovered, err = o.dailyPricingGroups(ctx, f, oraclePlanCovered)
	return out, err
}

func oracleGroupKeyExpr(g analytics.Group) string {
	if col := groupColumn(g); col != "" {
		return fmt.Sprintf("COALESCE(%s, '')", col)
	}
	return "''"
}

func oracleBucketExpr(widthSec int64) string {
	return fmt.Sprintf("(timestamp_ns / 1000000000 / %d) * %d", widthSec, widthSec)
}

func oracleCostSelection(sel oracleSelection) []string {
	if sel == oraclePlanCovered {
		return []string{costSourcePlanCovered}
	}
	return []string{"(cost_usd IS NULL OR cost_usd = 0)", oracleMetered}
}

func (o oracleStore) usageBuckets(ctx context.Context, f analytics.Filter, widthSec int64, group analytics.Group) ([]analytics.Row, error) {
	q, args := oracleUsageBucketsQuery(f, widthSec, group)
	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		r.BucketStart = time.Unix(bucketStartSec, 0).UTC()
		r.GroupKey = groupKey
		out = append(out, r)
	}
	return out, rows.Err()
}

func oracleUsageBucketsQuery(f analytics.Filter, widthSec int64, group analytics.Group) (string, []any) {
	conds, args := analyticsConditions(f)
	groupCol := groupColumn(group)
	selectCols := []string{oracleBucketExpr(widthSec) + " AS bucket_start_sec"}
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
	q := "SELECT " + strings.Join(selectCols, ", ") + " FROM events"
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

func (o oracleStore) bucketPricingGroups(ctx context.Context, f analytics.Filter, widthSec int64, group analytics.Group, sel oracleSelection) ([]analytics.PricingGroup, error) {
	q, args := oracleBucketPricingQuery(f, widthSec, group, sel)
	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		g.At = time.Unix(bucketSec, 0).UTC()
		g.Provider, g.Model = provider.String, model.String
		g.Events, g.InputTokens, g.OutputTokens = events.Int64, inTok.Int64, outTok.Int64
		g.CacheReadTokens, g.CacheWriteTokens, g.CacheWrite1hTokens = cacheIn.Int64, cacheW.Int64, cacheW1h.Int64
		out = append(out, g)
	}
	return out, rows.Err()
}

func oracleBucketPricingQuery(f analytics.Filter, widthSec int64, group analytics.Group, sel oracleSelection) (string, []any) {
	conds, args := analyticsConditions(f)
	conds = append(conds, oracleCostSelection(sel)...)
	q := "SELECT " + oracleBucketExpr(widthSec) + " AS bucket_start_sec, " + oracleGroupKeyExpr(group) + " AS group_key," +
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

func (o oracleStore) usageTotals(ctx context.Context, f analytics.Filter) (analytics.Summary, error) {
	q, args := oracleUsageTotalsQuery(f)
	var sum analytics.Summary
	err := o.db.QueryRowContext(ctx, q, args...).Scan(&sum.Requests, &sum.InputTokens, &sum.OutputTokens, &sum.TotalTokens, &sum.CostUSD)
	return sum, err
}

func oracleUsageTotalsQuery(f analytics.Filter) (string, []any) {
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

func (o oracleStore) dailyPricingGroups(ctx context.Context, f analytics.Filter, sel oracleSelection) ([]analytics.PricingGroup, error) {
	q, args := oracleDailyPricingQuery(f, sel)
	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		g.At = time.Unix(0, pricedAtNs).UTC()
		g.Provider, g.Model = provider.String, model.String
		g.Events, g.InputTokens, g.OutputTokens = requests.Int64, inTok.Int64, outTok.Int64
		g.CacheReadTokens, g.CacheWriteTokens, g.CacheWrite1hTokens = cacheIn.Int64, cacheW.Int64, cacheW1h.Int64
		out = append(out, g)
	}
	return out, rows.Err()
}

func oracleDailyPricingQuery(f analytics.Filter, sel oracleSelection) (string, []any) {
	order := ` ORDER BY provider, model`
	if sel == oraclePlanCovered {
		order = ""
	}
	conds, args := analyticsConditions(f)
	conds = append(conds, oracleCostSelection(sel)...)
	q := `SELECT ` + oraclePricedAtColumn + `, provider, model, COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(` + cacheReadExpr + `), 0),
			COALESCE(SUM(` + cacheWriteExpr + `), 0),
			COALESCE(SUM(` + cacheWrite1hExpr + `), 0)
		FROM events WHERE ` + strings.Join(conds, " AND ") +
		` GROUP BY ` + oracleDayBucketExpr + `, provider, model` + order
	return q, args
}
