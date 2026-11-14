package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventCache answers ReadEvents from memory, reading from the store only
// what changed since its last answer.
//
// The glance reads two to four weeks of prompt events in full: on a busy
// store that is tens of thousands of rows, each one's payload and
// attributes read and decoded, seconds per request, and the menu bar asks
// every minute. Between two asks a few dozen events arrive. EventCache
// keeps the decoded events and, on each read, fetches only the rows added
// since (by rowid) and the rows events_changes (migration 7) says were
// updated or deleted since, all inside one read transaction so the answer
// is one snapshot of the store. Whichever process changed them, the
// answer is the one Store.ReadEvents gives.
//
// How it tells what changed, and when it reads a window in full again,
// is windowCache's. Warm does the first read ahead of the first caller.
//
// The envelopes it returns are shared between reads and must not be
// modified. It holds at most maxSpan of events per type (a month of a busy
// machine's prompt events is ~180 MB decoded), and drops them all after
// idle without a read, so a daemon nobody is polling holds none.
type EventCache struct {
	windowCache[eventschema.EventType, cachedEvent]
}

// cachedEvent is one stored event and the row it was read from.
type cachedEvent struct {
	rowid int64
	env   *eventschema.Envelope
}

func (e cachedEvent) rowID() int64    { return e.rowid }
func (e cachedEvent) unixNano() int64 { return e.env.Timestamp.UnixNano() }

// Defaults for NewEventCache: a month and a little covers the glance's
// furthest window (the start of the month or two weeks back), and the menu
// bar reads every minute.
const (
	DefaultCacheSpan = 32 * 24 * time.Hour
	DefaultCacheIdle = 10 * time.Minute
)

// maxCacheSpan is the longest span a cache may hold. Changes to events
// older than changeLogHorizon are not logged, so the span stays inside it,
// with days to spare for a clock set back.
const maxCacheSpan = changeLogHorizon - 5*24*time.Hour

// NewEventCache caches store's events for windows up to maxSpan back,
// dropping them after idle without a read. Zero values take the defaults;
// a span beyond what the store logs changes for is shortened to it.
func NewEventCache(store *Store, maxSpan, idle time.Duration) *EventCache {
	if maxSpan <= 0 {
		maxSpan = DefaultCacheSpan
	}
	if idle <= 0 {
		idle = DefaultCacheIdle
	}
	c := &EventCache{}
	c.init(store, windowReads[eventschema.EventType, cachedEvent]{
		full: readCachedEvents, past: readNewEvents, changed: readChangedEvents, compare: compareCached,
	}, maxSpan, idle)
	return c
}

// CountBySource is Store.CountBySource; it is a single indexed query.
func (c *EventCache) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	return c.store.CountBySource(ctx, since, until)
}

// LatestAttributesBySource is Store.LatestAttributesBySource: one indexed
// query, uncached.
func (c *EventCache) LatestAttributesBySource(ctx context.Context, source, key string, since time.Time) (map[string]string, time.Time, bool, error) {
	return c.store.LatestAttributesBySource(ctx, source, key, since)
}

// AttributesBySourceBetween is Store.AttributesBySourceBetween: one indexed
// query, uncached.
func (c *EventCache) AttributesBySourceBetween(ctx context.Context, source, key string, since, until time.Time) (map[string]string, time.Time, bool, error) {
	return c.store.AttributesBySourceBetween(ctx, source, key, since, until)
}

// Warm reads t's events from since into the cache, so the first caller
// does not wait for the full read. Like a read, it starts the idle
// countdown.
func (c *EventCache) Warm(ctx context.Context, t eventschema.EventType, since time.Time) error {
	_, err := c.ReadEvents(ctx, t, since)
	return err
}

// ReadEvents returns what Store.ReadEvents returns: every event of type t
// at or after since, oldest first.
func (c *EventCache) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	ok, err := c.view(ctx, t, since.UTC().UnixNano(), func(rows []cachedEvent) {
		out = make([]*eventschema.Envelope, len(rows))
		for i, e := range rows {
			out[i] = e.env
		}
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return c.store.ReadEvents(ctx, t, since)
	}
	return out, nil
}

// cachedSelectSQL is selectSQL with each row's rowid first.
var cachedSelectSQL = strings.Replace(selectSQL, "SELECT", "SELECT rowid,", 1)

// readCachedEvents is readEvents keeping each event's rowid, each event
// readied for the cache by in.
func readCachedEvents(ctx context.Context, q querier, in *interner, t eventschema.EventType, from int64) ([]cachedEvent, error) {
	var out []cachedEvent
	err := readEventPages(ctx, q, cachedSelectSQL, t, time.Unix(0, from), func(rs *sql.Rows) (*eventschema.Envelope, error) {
		var e cachedEvent
		var err error
		if e.env, err = scanEnvelope(rs, &e.rowid); err == nil {
			in.cached(e.env)
			out = append(out, e)
		}
		return e.env, err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanCachedEvents decodes every row of rs, a cachedSelectSQL, each
// event readied for the cache by in.
func scanCachedEvents(rs *sql.Rows, in *interner) ([]cachedEvent, error) {
	var out []cachedEvent
	for rs.Next() {
		var e cachedEvent
		var err error
		if e.env, err = scanEnvelope(rs, &e.rowid); err != nil {
			return nil, err
		}
		in.cached(e.env)
		out = append(out, e)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: rows: %w", err)
	}
	return out, nil
}

// newEventsSQL reads the rows past a rowid, by rowid (windowReads.past).
var newEventsSQL = strings.Replace(cachedSelectSQL, "FROM events", "FROM events NOT INDEXED", 1) + `
WHERE rowid > ? AND type = ? AND timestamp_ns >= ?`

// changedEventsSQL re-reads the logged rows at or below a rowid, each by
// rowid as in newEventsSQL.
var changedEventsSQL = strings.Replace(cachedSelectSQL, "FROM events", "FROM events NOT INDEXED", 1) + `
WHERE ` + changedRowIDs + ` AND type = ? AND timestamp_ns >= ?`

// readNewEvents is windowReads.past for events.
func readNewEvents(ctx context.Context, q querier, in *interner, t eventschema.EventType, known, from int64) ([]cachedEvent, error) {
	rs, err := q.QueryContext(ctx, newEventsSQL, known, string(t), from)
	if err != nil {
		return nil, fmt.Errorf("sqlite: event cache: new events: %w", err)
	}
	defer func() { _ = rs.Close() }()
	return scanCachedEvents(rs, in)
}

// readChangedEvents is windowReads.changed for events.
func readChangedEvents(ctx context.Context, q querier, in *interner, t eventschema.EventType, seq, known, from int64) ([]cachedEvent, error) {
	rs, err := q.QueryContext(ctx, changedEventsSQL, seq, known, string(t), from)
	if err != nil {
		return nil, fmt.Errorf("sqlite: event cache: changed events: %w", err)
	}
	defer func() { _ = rs.Close() }()
	return scanCachedEvents(rs, in)
}

// compareCached is ReadEvents' order: timestamp, then id.
func compareCached(a, b cachedEvent) int {
	an, bn := a.env.Timestamp.UnixNano(), b.env.Timestamp.UnixNano()
	switch {
	case an < bn:
		return -1
	case an > bn:
		return 1
	default:
		return strings.Compare(a.env.ID, b.env.ID)
	}
}

// uncachedAttributes are the attributes the readers' writers record that
// no reader of the cache asks for: per-turn token splits and record
// bookkeeping, also kept in the payload or never read back. Readers that
// need them read the store.
//
// The cache holds a month of prompt events, most of them Codex turns
// carrying 17 attributes each; their maps were most of the daemon's heap
// (about 500 MB of 1.1 GB on a busy machine), and the collector's work
// grows with it. Leaving these out takes a Codex turn to 13, half the
// map's storage. A key is only listed here when no code outside the
// readers that write it reads it: the readers that match keys by suffix
// (_used_pct, _reset_at) or by prefix (allowance_) are why this is a list
// of keys to leave out, not of keys to keep.
var uncachedAttributes = []string{
	"cached_input", "reasoning_output", "sequence", "model_provider", "message_id",
	"input_uncached", "cache_creation_input", "service_tier", "subagent_tool_use_id",
}
