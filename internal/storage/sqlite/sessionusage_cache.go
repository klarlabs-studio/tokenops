package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SessionUsageCache is the store as analytics.Store, answering
// SessionUsage from memory and reading from the store only what changed
// since its last answer (windowCache). The other reads go to the store.
//
// The hourly team upload, the waste report it carries and the anomaly
// read each list every session turn of the last month: on a busy machine
// that is hundreds of thousands of rows, each one's payload and attributes
// read through json_extract, tens of seconds per upload. Between two
// uploads a few hundred turns arrive.
//
// It answers a Filter narrowed by Since, Until and the excluded sources
// only, from no further back than its span; any other read, and any read
// on a store without a change log, it hands to the store. Its answer is
// the store's, in the store's order.
//
// A row it holds is a few numbers and a pointer to the strings it shares
// with the other turns of its session (sessionKey): no payload, no
// attributes. It drops them all after idle without a read.
type SessionUsageCache struct {
	windowCache[struct{}, sessionRow]
}

var _ analytics.Store = (*SessionUsageCache)(nil)

// DefaultSessionUsageIdle is how long a SessionUsageCache holds its rows
// without a read: past the hourly upload's interval, so its rows are
// there for the next one.
const DefaultSessionUsageIdle = 2 * time.Hour

// NewSessionUsageCache caches store's session usage for windows up to
// maxSpan back, dropping it after idle without a read. Zero values take
// the longest span the store logs changes for and
// DefaultSessionUsageIdle.
func NewSessionUsageCache(store *Store, maxSpan, idle time.Duration) *SessionUsageCache {
	if maxSpan <= 0 {
		maxSpan = maxCacheSpan
	}
	if idle <= 0 {
		idle = DefaultSessionUsageIdle
	}
	c := &SessionUsageCache{}
	c.init(store, windowReads[struct{}, sessionRow]{
		full: readSessionRows, past: readNewSessionRows, changed: readChangedSessionRows, compare: compareSessionRows,
	}, maxSpan, idle)
	return c
}

// SessionUsage returns what Store.SessionUsage returns.
func (c *SessionUsageCache) SessionUsage(ctx context.Context, f analytics.Filter) ([]analytics.SessionUsage, error) {
	excluded, ok := cachedSessionFilter(f)
	if !ok {
		return c.store.SessionUsage(ctx, f)
	}
	until := int64(0)
	if !f.Until.IsZero() {
		until = f.Until.UTC().UnixNano()
	}
	var out []analytics.SessionUsage
	answered, err := c.view(ctx, struct{}{}, f.Since.UTC().UnixNano(), func(rows []sessionRow) {
		if len(rows) > 0 {
			// At most every row: one allocation, not a run of doublings.
			out = make([]analytics.SessionUsage, 0, len(rows))
		}
		for i := range rows {
			r := &rows[i]
			if until != 0 && r.at >= until {
				break
			}
			if len(excluded) > 0 && slices.Contains(excluded, r.key.source) {
				continue
			}
			out = append(out, r.usage())
		}
	})
	if err != nil {
		return nil, err
	}
	if !answered {
		return c.store.SessionUsage(ctx, f)
	}
	if len(out) == 0 {
		// The store answers nothing as nil.
		return nil, nil
	}
	return out, nil
}

// BucketUsage is Store.BucketUsage, uncached.
func (c *SessionUsageCache) BucketUsage(ctx context.Context, f analytics.Filter, widthSec int64, group analytics.Group) (analytics.BucketUsage, error) {
	return c.store.BucketUsage(ctx, f, widthSec, group)
}

// WindowUsage is Store.WindowUsage, uncached.
func (c *SessionUsageCache) WindowUsage(ctx context.Context, f analytics.Filter) (analytics.WindowUsage, error) {
	return c.store.WindowUsage(ctx, f)
}

// CacheTotals is Store.CacheTotals, uncached.
func (c *SessionUsageCache) CacheTotals(ctx context.Context, f analytics.Filter) (analytics.CacheTotals, error) {
	return c.store.CacheTotals(ctx, f)
}

// cachedSessionFilter reports whether the cache answers f, and the
// sources it excludes. A source excluded as "" is left to the store, which
// tells a NULL source (kept) from an empty one (excluded); the cache holds
// both as "".
func cachedSessionFilter(f analytics.Filter) ([]string, bool) {
	if f.Since.IsZero() || (f.EventType != "" && f.EventType != eventschema.EventTypePrompt) ||
		f.Provider != "" || f.Model != "" || f.WorkflowID != "" || f.AgentID != "" {
		return nil, false
	}
	excluded := f.ExcludedSources()
	if slices.Contains(excluded, "") {
		return nil, false
	}
	return excluded, true
}

// sessionRow is one session turn as the cache holds it.
type sessionRow struct {
	rowid, at int64
	key       *sessionKey
	// in includes the cache portions.
	in, out, total, cacheRead, cacheWrite, cacheWrite1h int64
	cost                                                float64
	latency                                             int64
	planCovered                                         bool
}

// sessionKey is what a session's turns share: most of a session's turns
// name the same session, model, client and route.
type sessionKey struct {
	session, provider, model, workflow, execution, source, endpoint string
}

func (r *sessionRow) usage() analytics.SessionUsage {
	return analytics.SessionUsage{
		SessionID: r.key.session, At: time.Unix(0, r.at).UTC(), Provider: r.key.provider, Model: r.key.model,
		InputTokens: r.in, OutputTokens: r.out, TotalTokens: r.total,
		CacheReadTokens: r.cacheRead, CacheWriteTokens: r.cacheWrite, CacheWrite1hTokens: r.cacheWrite1h,
		CostUSD: r.cost, WorkflowID: r.key.workflow, ExecutionID: r.key.execution,
		Source: r.key.source, Endpoint: r.key.endpoint, PlanCovered: r.planCovered, Latency: time.Duration(r.latency),
	}
}

func (r sessionRow) rowID() int64    { return r.rowid }
func (r sessionRow) unixNano() int64 { return r.at }

// compareSessionRows is SessionUsage's order: timestamp, then rowid.
func compareSessionRows(a, b sessionRow) int {
	switch {
	case a.at < b.at:
		return -1
	case a.at > b.at:
		return 1
	case a.rowid < b.rowid:
		return -1
	case a.rowid > b.rowid:
		return 1
	}
	return 0
}

// sessionRowsSQL selects the rows the cache holds: every usage-carrying
// prompt event of a session, its rowid first.
const sessionRowsSQL = `SELECT rowid, ` + sessionUsageColumns + ` FROM events`

// sessionRowsWhere are the rows the cache holds; its argument is the
// prompt type.
const sessionRowsWhere = ` AND type = ? AND ` + usageOnly + ` AND ` + hasSession + ` AND timestamp_ns >= ?`

var (
	fullSessionRowsSQL    = sessionRowsSQL + ` WHERE 1` + sessionRowsWhere + ` ORDER BY timestamp_ns, rowid`
	newSessionRowsSQL     = sessionRowsSQL + ` NOT INDEXED WHERE rowid > ?` + sessionRowsWhere
	changedSessionRowsSQL = sessionRowsSQL + ` NOT INDEXED WHERE ` + changedRowIDs + sessionRowsWhere
)

// readSessionRows is windowReads.full for session usage.
func readSessionRows(ctx context.Context, q querier, in *interner, _ struct{}, from int64) ([]sessionRow, error) {
	return querySessionRows(ctx, q, in, fullSessionRowsSQL, string(eventschema.EventTypePrompt), from)
}

// readNewSessionRows is windowReads.past for session usage.
func readNewSessionRows(ctx context.Context, q querier, in *interner, _ struct{}, known, from int64) ([]sessionRow, error) {
	return querySessionRows(ctx, q, in, newSessionRowsSQL, known, string(eventschema.EventTypePrompt), from)
}

// readChangedSessionRows is windowReads.changed for session usage.
func readChangedSessionRows(ctx context.Context, q querier, in *interner, _ struct{}, seq, known, from int64) ([]sessionRow, error) {
	return querySessionRows(ctx, q, in, changedSessionRowsSQL, seq, known, string(eventschema.EventTypePrompt), from)
}

func querySessionRows(ctx context.Context, q querier, in *interner, query string, args ...any) ([]sessionRow, error) {
	rs, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: session usage cache: %w", err)
	}
	defer func() { _ = rs.Close() }()
	return scanSessionRows(rs, in)
}

func scanSessionRows(rs *sql.Rows, in *interner) ([]sessionRow, error) {
	var out []sessionRow
	for rs.Next() {
		var rowid int64
		u, err := scanSessionUsage(rs, &rowid)
		if err != nil {
			return nil, err
		}
		out = append(out, sessionRow{
			rowid: rowid, at: u.At.UnixNano(),
			key: in.sessionKey(sessionKey{session: u.SessionID, provider: u.Provider, model: u.Model,
				workflow: u.WorkflowID, execution: u.ExecutionID, source: u.Source, endpoint: u.Endpoint}),
			in: u.InputTokens, out: u.OutputTokens, total: u.TotalTokens,
			cacheRead: u.CacheReadTokens, cacheWrite: u.CacheWriteTokens, cacheWrite1h: u.CacheWrite1hTokens,
			cost: u.CostUSD, latency: int64(u.Latency), planCovered: u.PlanCovered,
		})
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: session usage cache: %w", err)
	}
	return out, nil
}

// sessionKey is k, as the copy handed out first.
func (in *interner) sessionKey(k sessionKey) *sessionKey {
	if c, ok := in.keys[k]; ok {
		return c
	}
	in.room()
	c := &sessionKey{session: in.str(k.session), provider: in.str(k.provider), model: in.str(k.model),
		workflow: in.str(k.workflow), execution: in.str(k.execution), source: in.str(k.source), endpoint: in.str(k.endpoint)}
	in.keys[*c] = c
	return c
}
