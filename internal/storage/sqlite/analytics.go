package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// *Store is the analytics aggregator's read side: it implements
// analytics.Store over the events table. The sums and groupings are the
// store's; pricing them, and accounting for what could not be priced, is
// the aggregator's.
var _ analytics.Store = (*Store)(nil)

// Stored events carry the cache portions of their input in the payload.
// Claude Code events written before the payload had cache-write fields
// recorded the writes as the cache_creation_input attribute, inside an
// InputTokens that already included them, so they are read as writes too.
// No other source recorded writes inside InputTokens before the payload
// field existed, so no other attribute is a safe fallback.
//
// These expressions, and the cost-source one below, are indexed verbatim
// by events_usage_idx (migration 5) so the rollups never read the table.
// Change one and the index stops covering it: change both together.
const (
	cacheReadExpr    = `CAST(COALESCE(json_extract(payload, '$.cached_input_tokens'), json_extract(attributes, '$.cache_read_input')) AS INTEGER)`
	cacheWriteExpr   = `CAST(COALESCE(json_extract(payload, '$.cache_write_input_tokens'), json_extract(attributes, '$.cache_creation_input')) AS INTEGER)`
	cacheWrite1hExpr = `CAST(json_extract(payload, '$.cache_write_1h_input_tokens') AS INTEGER)`
)

// costSourcePlanCovered selects the plan-included and trial traffic: what
// the API-equivalent figure values at list price. Everything else is
// metered. Plan-included and trial events are zero-cost BY DESIGN (the
// request is covered by a subscription or vendor credit), so cost
// RECOMPUTE stays away from them: repricing them at list rates would
// invent spend. Pricing GAPS in plan-covered traffic are still reported,
// because on a subscription that traffic is the majority and an unpriced
// model there would otherwise leave no trace at all. The schema column
// carries only the bundled counters, so the source is read from payload
// JSON.
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

// How an event's cost is accounted, as usageScanQuery classifies it.
const (
	// accountedStored is metered traffic stored with a cost: authoritative.
	accountedStored int64 = iota
	// accountedUncosted is metered traffic stored without one, which the
	// aggregator prices from the rate card.
	accountedUncosted
	// accountedPlanCovered is plan-included and trial traffic.
	accountedPlanCovered
)

// usageScanQuery is the one statement BucketUsage and WindowUsage read:
// each matching event's usage, straight off events_usage_idx.
//
// The rollups used to ask SQLite for three GROUP BYs of the same window —
// the usage sums and the two kinds of pricing group — and each sorted
// every row it grouped in a temporary b-tree: the sort, not the index
// read, was most of a request. Hashing the rows in Go reads the index
// once and sorts only the groups. The group-by key for workflow and agent
// is read here; provider and model already are.
func usageScanQuery(f analytics.Filter, group analytics.Group) (string, []any) {
	conds, args := analyticsConditions(f)
	key := "''"
	if group == analytics.GroupWorkflow || group == analytics.GroupAgent {
		key = "COALESCE(" + groupColumn(group) + ", '')"
	}
	q := `SELECT timestamp_ns, provider, model, ` + key + `,
		CASE WHEN ` + costSourcePlanCovered + ` THEN ` + fmt.Sprint(accountedPlanCovered) + `
			WHEN cost_usd IS NULL OR cost_usd = 0 THEN ` + fmt.Sprint(accountedUncosted) + `
			ELSE ` + fmt.Sprint(accountedStored) + ` END,
		COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(total_tokens, 0), cost_usd,
		COALESCE(` + cacheReadExpr + `, 0),
		COALESCE(` + cacheWriteExpr + `, 0),
		COALESCE(` + cacheWrite1hExpr + `, 0)
		FROM events WHERE ` + strings.Join(conds, " AND ")
	return q, args
}

// usageRow is one event as usageScanQuery reads it.
type usageRow struct {
	ts              int64
	provider, model sql.NullString
	key             string
	accounted       int64
	in, out, total  int64
	cost            sql.NullFloat64
	cacheRead       int64
	cacheWrite      int64
	cacheWrite1h    int64
}

// scanUsage reads every event usageScanQuery selects, in index order, and
// hands each to visit. The row is reused between calls.
func (s *Store) scanUsage(ctx context.Context, f analytics.Filter, group analytics.Group, visit func(*usageRow)) error {
	q, args := usageScanQuery(f, group)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("analytics: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var r usageRow
	for rows.Next() {
		if err := rows.Scan(&r.ts, &r.provider, &r.model, &r.key, &r.accounted,
			&r.in, &r.out, &r.total, &r.cost, &r.cacheRead, &r.cacheWrite, &r.cacheWrite1h); err != nil {
			return fmt.Errorf("analytics: scan: %w", err)
		}
		switch group {
		case analytics.GroupProvider:
			r.key = r.provider.String
		case analytics.GroupModel:
			r.key = r.model.String
		}
		visit(&r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("analytics: iterate: %w", err)
	}
	return nil
}

// sqlSum is SQLite's SUM() over REAL values, reproduced bit for bit: the
// rollups' costs were SQLite sums, and an answer that moved in its last
// digit would be a different answer. SQLite (3.43 on) sums floats with
// Kahan-Babuska-Neumaier compensation and returns the sum plus the
// compensation, or the bare sum when the compensation overflowed.
type sqlSum struct {
	sum, err float64
	n        int64
}

func (s *sqlSum) add(r float64) {
	s.n++
	t := s.sum + r
	if math.Abs(s.sum) > math.Abs(r) {
		s.err += (s.sum - t) + r
	} else {
		s.err += (r - t) + s.sum
	}
	s.sum = t
}

// value is COALESCE(SUM(x), 0).
func (s *sqlSum) value() float64 {
	if s.n == 0 {
		return 0
	}
	if math.IsInf(s.err, 0) || math.IsNaN(s.err) {
		return s.sum
	}
	return s.sum + s.err
}

// pricingKey identifies a pricing group. provider and model keep NULL
// apart from the empty string: SQLite grouped them separately, and each group is priced
// on its own.
type pricingKey struct {
	at              int64
	key             string
	provider, model sql.NullString
}

// pricingSums accumulates one pricing group.
type pricingSums struct {
	g      analytics.PricingGroup
	latest int64
}

func (p *pricingSums) add(r *usageRow) {
	if p.g.Events == 0 || r.ts > p.latest {
		p.latest = r.ts
	}
	p.g.Events++
	p.g.InputTokens += r.in
	p.g.OutputTokens += r.out
	p.g.CacheReadTokens += r.cacheRead
	p.g.CacheWriteTokens += r.cacheWrite
	p.g.CacheWrite1hTokens += r.cacheWrite1h
}

// pricingGroups accumulates the two kinds of pricing group.
type pricingGroups struct {
	uncosted, planCovered map[pricingKey]*pricingSums
}

func newPricingGroups() pricingGroups {
	return pricingGroups{uncosted: map[pricingKey]*pricingSums{}, planCovered: map[pricingKey]*pricingSums{}}
}

// add counts r in its group at k, if it is priced from the rate card.
func (pg pricingGroups) add(k pricingKey, r *usageRow) {
	var m map[pricingKey]*pricingSums
	switch r.accounted {
	case accountedUncosted:
		m = pg.uncosted
	case accountedPlanCovered:
		m = pg.planCovered
	default:
		return
	}
	k.provider, k.model = r.provider, r.model
	p := m[k]
	if p == nil {
		p = &pricingSums{}
		m[k] = p
	}
	p.add(r)
}

// compareNullText orders as SQLite does: NULL first, then text bytewise.
func compareNullText(a, b sql.NullString) int {
	switch {
	case a.Valid != b.Valid:
		if a.Valid {
			return 1
		}
		return -1
	default:
		return strings.Compare(a.String, b.String)
	}
}

// sortedGroups lists m's groups in the order cmp gives their keys, each
// finished by done. An empty map lists nil, as an empty query did.
func sortedGroups(m map[pricingKey]*pricingSums, cmp func(a, b pricingKey) int, done func(pricingKey, *pricingSums) analytics.PricingGroup) []analytics.PricingGroup {
	if len(m) == 0 {
		return nil
	}
	keys := make([]pricingKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, cmp)
	out := make([]analytics.PricingGroup, len(keys))
	for i, k := range keys {
		out[i] = done(k, m[k])
	}
	return out
}

// bucketKey identifies a BucketUsage row.
type bucketKey struct {
	bucket int64
	key    string
}

// bucketSums accumulates one BucketUsage row.
type bucketSums struct {
	row  analytics.Row
	cost sqlSum
}

// BucketUsage implements analytics.Store: one read of the window, grouped
// as GROUP BY (timestamp_ns / 1e9 / width) * width, group key — and for
// the pricing groups provider and model too — would group it.
func (s *Store) BucketUsage(ctx context.Context, f analytics.Filter, widthSec int64, group analytics.Group) (analytics.BucketUsage, error) {
	if s == nil {
		return analytics.BucketUsage{}, analytics.ErrNotInitialised
	}
	if widthSec <= 0 {
		return analytics.BucketUsage{}, fmt.Errorf("analytics: invalid bucket width %d", widthSec)
	}
	buckets := map[bucketKey]*bucketSums{}
	pricing := newPricingGroups()
	err := s.scanUsage(ctx, f, group, func(r *usageRow) {
		bk := bucketKey{bucket: r.ts / 1_000_000_000 / widthSec * widthSec, key: r.key}
		b := buckets[bk]
		if b == nil {
			b = &bucketSums{}
			buckets[bk] = b
		}
		b.row.Requests++
		b.row.InputTokens += r.in
		b.row.OutputTokens += r.out
		b.row.TotalTokens += r.total
		if r.cost.Valid {
			b.cost.add(r.cost.Float64)
		}
		pricing.add(pricingKey{at: bk.bucket, key: bk.key}, r)
	})
	if err != nil {
		return analytics.BucketUsage{}, err
	}

	var out analytics.BucketUsage
	if len(buckets) > 0 {
		keys := make([]bucketKey, 0, len(buckets))
		for k := range buckets {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, func(a, b bucketKey) int {
			return cmp.Or(cmp.Compare(a.bucket, b.bucket), strings.Compare(a.key, b.key))
		})
		out.Rows = make([]analytics.Row, len(keys))
		for i, k := range keys {
			b := buckets[k]
			row := b.row
			row.BucketStart = time.Unix(k.bucket, 0).UTC()
			row.GroupKey = k.key
			row.CostUSD = b.cost.value()
			out.Rows[i] = row
		}
	}
	byBucket := func(a, b pricingKey) int {
		return cmp.Or(cmp.Compare(a.at, b.at), strings.Compare(a.key, b.key),
			compareNullText(a.provider, b.provider), compareNullText(a.model, b.model))
	}
	done := func(k pricingKey, p *pricingSums) analytics.PricingGroup {
		g := p.g
		g.At = time.Unix(k.at, 0).UTC()
		g.GroupKey = k.key
		g.Provider, g.Model = k.provider.String, k.model.String
		return g
	}
	out.Uncosted = sortedGroups(pricing.uncosted, byBucket, done)
	out.PlanCovered = sortedGroups(pricing.planCovered, byBucket, done)
	return out, nil
}

// nsPerDay is a UTC day in nanoseconds: the daily pricing groups' grain,
// the finest a rate card changes at in practice.
const nsPerDay = 86_400 * 1_000_000_000

// WindowUsage implements analytics.Store: one read of the window, its
// pricing groups grouped per (UTC day, provider, model).
func (s *Store) WindowUsage(ctx context.Context, f analytics.Filter) (analytics.WindowUsage, error) {
	if s == nil {
		return analytics.WindowUsage{}, analytics.ErrNotInitialised
	}
	var (
		totals analytics.Summary
		cost   sqlSum
	)
	pricing := newPricingGroups()
	err := s.scanUsage(ctx, f, analytics.GroupNone, func(r *usageRow) {
		totals.Requests++
		totals.InputTokens += r.in
		totals.OutputTokens += r.out
		totals.TotalTokens += r.total
		if r.cost.Valid {
			cost.add(r.cost.Float64)
		}
		pricing.add(pricingKey{at: r.ts / nsPerDay}, r)
	})
	if err != nil {
		return analytics.WindowUsage{}, err
	}
	totals.CostUSD = cost.value()

	done := func(k pricingKey, p *pricingSums) analytics.PricingGroup {
		g := p.g
		g.At = time.Unix(0, p.latest).UTC()
		g.Provider, g.Model = k.provider.String, k.model.String
		return g
	}
	return analytics.WindowUsage{
		Totals: totals,
		// The uncosted groups were ordered by provider and model, each
		// one's days in grouping order.
		Uncosted: sortedGroups(pricing.uncosted, func(a, b pricingKey) int {
			return cmp.Or(compareNullText(a.provider, b.provider), compareNullText(a.model, b.model), cmp.Compare(a.at, b.at))
		}, done),
		PlanCovered: sortedGroups(pricing.planCovered, func(a, b pricingKey) int {
			return cmp.Or(cmp.Compare(a.at, b.at), compareNullText(a.provider, b.provider), compareNullText(a.model, b.model))
		}, done),
	}, nil
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
