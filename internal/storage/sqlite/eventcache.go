package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
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
// Rows are found by rowid, which SQLite gives each new row past the
// largest. Deleting the newest rows lets the next insert reuse their
// rowids, and a VACUUM may renumber them (without firing a trigger): so
// the cache remembers the store's newest rows, and treats every row past
// the newest of them still holding its id as unknown and reads it again.
// A VACUUM renumbers in rowid order, closing gaps: a row that kept its
// rowid had no gap below it, so every row below it kept its rowid too.
//
// It reads the window in full again only when it cannot tell what
// changed: the first read, a read reaching further back than it holds, a
// change log that moved on past it, more changes than events held, or
// none of the newest rows left under their rowid. Warm does the first
// read ahead of the first caller.
//
// The envelopes it returns are shared between reads and must not be
// modified. It holds at most maxSpan of events per type (a month of a busy
// machine's prompt events is ~180 MB decoded), and drops them all after
// idle without a read, so a daemon nobody is polling holds none.
type EventCache struct {
	store *Store
	// maxSpan is the oldest window kept: a read reaching further back goes
	// to the store uncached.
	maxSpan time.Duration
	// idle is how long the events outlive the last read.
	idle time.Duration
	now  func() time.Time

	mu    sync.Mutex
	types map[eventschema.EventType]*cachedEvents
	timer *time.Timer
	// fullReads counts the windows read in full, for tests.
	fullReads int
}

// cachedEvents is one event type's window as of a snapshot.
type cachedEvents struct {
	// floor is the earliest timestamp held; every stored event of the
	// type at or after it is in events.
	floor int64
	// seq is the last events_changes entry the window reflects.
	seq int64
	// tail is the newest rows in the store at the snapshot, newest first:
	// rows after the newest one still there are new.
	tail []tailRow
	// events is ordered as ReadEvents orders: by timestamp, then id.
	events []cachedEvent
}

// cachedEvent is one stored event and the row it was read from.
type cachedEvent struct {
	rowid int64
	env   *eventschema.Envelope
}

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
	maxSpan = min(maxSpan, maxCacheSpan)
	if idle <= 0 {
		idle = DefaultCacheIdle
	}
	return &EventCache{store: store, maxSpan: maxSpan, idle: idle, now: time.Now,
		types: map[eventschema.EventType]*cachedEvents{}}
}

// CountBySource is Store.CountBySource; it is a single indexed query.
func (c *EventCache) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	return c.store.CountBySource(ctx, since, until)
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
	from := since.UTC().UnixNano()
	oldest := c.now().Add(-c.maxSpan).UnixNano()
	if from < oldest {
		return c.store.ReadEvents(ctx, t, since)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.touch()
	cached, err := c.refresh(ctx, t, from, oldest)
	if errors.Is(err, errNoChangeLog) {
		// A store without migration 7 cannot say what changed.
		return c.store.ReadEvents(ctx, t, since)
	}
	if err != nil {
		return nil, err
	}
	i := sort.Search(len(cached.events), func(i int) bool { return cached.events[i].env.Timestamp.UnixNano() >= from })
	out := make([]*eventschema.Envelope, len(cached.events)-i)
	for j, e := range cached.events[i:] {
		out[j] = e.env
	}
	return out, nil
}

// touch restarts the idle countdown. c.mu is held.
func (c *EventCache) touch() {
	if c.timer != nil {
		c.timer.Reset(c.idle)
		return
	}
	c.timer = time.AfterFunc(c.idle, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.types = map[eventschema.EventType]*cachedEvents{}
	})
}

// errNoChangeLog marks a store without events_changes.
var errNoChangeLog = errors.New("sqlite: store has no events_changes")

// refresh brings t's window up to date with the store and returns it,
// reaching back to from at least. c.mu is held.
func (c *EventCache) refresh(ctx context.Context, t eventschema.EventType, from, oldest int64) (*cachedEvents, error) {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: event cache: begin: %w", err)
	}
	// Read-only: rolling back ends the snapshot.
	defer func() { _ = tx.Rollback() }()

	snap, err := snapshotOf(ctx, tx)
	if err != nil {
		return nil, err
	}
	if cur := c.types[t]; cur != nil && cur.floor <= from {
		updated, err := cur.update(ctx, tx, t, snap)
		if err != nil {
			return nil, err
		}
		if updated {
			cur.trim(oldest)
			return cur, nil
		}
	}
	events, err := readCachedEvents(ctx, tx, t, from)
	if err != nil {
		return nil, err
	}
	c.fullReads++
	fresh := &cachedEvents{floor: from, seq: snap.seq, tail: snap.tail, events: events}
	c.types[t] = fresh
	return fresh, nil
}

// snapshot is what a read transaction saw of the store's changes.
type snapshot struct {
	// seq is the latest events_changes entry ever written, and firstSeq
	// the oldest the log still holds (0 when it holds none).
	seq, firstSeq int64
	// tail is the store's newest rows, newest first.
	tail []tailRow
}

// tailRow is a row of the store as a snapshot saw it.
type tailRow struct {
	rowid int64
	id    string
}

// tailRows is how many of the newest rows a snapshot remembers. Deleting
// all of them before the next read makes the cache read its window again.
const tailRows = 32

func snapshotOf(ctx context.Context, q querier) (snapshot, error) {
	var s snapshot
	// Two subqueries: SQLite answers a lone MIN or MAX from the end of
	// the index, but both in one SELECT by scanning the table. The
	// sequence, not MAX(seq), is the latest ever written: it holds even
	// if the log's rows were deleted.
	rows, err := q.QueryContext(ctx, `SELECT
		(SELECT COALESCE(MIN(seq), 0) FROM events_changes),
		COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'events_changes'), 0)`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return s, fmt.Errorf("%w: %w", errNoChangeLog, err)
		}
		return s, fmt.Errorf("sqlite: event cache: change log: %w", err)
	}
	if rows.Next() {
		err = rows.Scan(&s.firstSeq, &s.seq)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return s, fmt.Errorf("sqlite: event cache: change log: %w", err)
	}
	rows, err = q.QueryContext(ctx, `SELECT rowid, id FROM events ORDER BY rowid DESC LIMIT ?`, tailRows)
	if err != nil {
		return s, fmt.Errorf("sqlite: event cache: newest rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r tailRow
		if err := rows.Scan(&r.rowid, &r.id); err != nil {
			return s, fmt.Errorf("sqlite: event cache: newest rows: %w", err)
		}
		s.tail = append(s.tail, r)
	}
	return s, rows.Err()
}

// knownThrough is the rowid of the newest of tail still holding its id:
// every row at or below it is one the snapshot saw, under the rowid it
// saw. ok is false when none is, and the snapshot no longer says which
// rows are new. An empty tail (an empty store) knows of no rows: every
// row is new.
func knownThrough(ctx context.Context, q querier, tail []tailRow) (rowid int64, ok bool, err error) {
	if len(tail) == 0 {
		return 0, true, nil
	}
	args := make([]any, len(tail))
	for i, r := range tail {
		args[i] = r.rowid
	}
	rows, err := q.QueryContext(ctx, `SELECT rowid, id FROM events WHERE rowid IN (?`+
		strings.Repeat(", ?", len(tail)-1)+`)`, args...)
	if err != nil {
		return 0, false, fmt.Errorf("sqlite: event cache: newest rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	now := make(map[int64]string, len(tail))
	for rows.Next() {
		var r tailRow
		if err := rows.Scan(&r.rowid, &r.id); err != nil {
			return 0, false, fmt.Errorf("sqlite: event cache: newest rows: %w", err)
		}
		now[r.rowid] = r.id
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("sqlite: event cache: newest rows: %w", err)
	}
	for _, r := range tail {
		if id, there := now[r.rowid]; there && id == r.id {
			return r.rowid, true, nil
		}
	}
	return 0, false, nil
}

// cachedSelectSQL is selectSQL with each row's rowid first.
var cachedSelectSQL = strings.Replace(selectSQL, "SELECT", "SELECT rowid,", 1)

// readCachedEvents is readEvents keeping each event's rowid.
func readCachedEvents(ctx context.Context, q querier, t eventschema.EventType, from int64) ([]cachedEvent, error) {
	var out []cachedEvent
	err := readEventPages(ctx, q, cachedSelectSQL, t, time.Unix(0, from), func(rs *sql.Rows) (*eventschema.Envelope, error) {
		var e cachedEvent
		var err error
		if e.env, err = scanEnvelope(rs, &e.rowid); err == nil {
			out = append(out, e)
		}
		return e.env, err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanCachedEvents decodes every row of rs, a cachedSelectSQL.
func scanCachedEvents(rs *sql.Rows) ([]cachedEvent, error) {
	var out []cachedEvent
	for rs.Next() {
		var e cachedEvent
		var err error
		if e.env, err = scanEnvelope(rs, &e.rowid); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: rows: %w", err)
	}
	return out, nil
}

// newEventsSQL reads the rows past a rowid. NOT INDEXED keeps SQLite on
// the rowid seek: left to choose, it walks the (type, timestamp) index
// across the whole window, the read the cache exists to avoid.
var newEventsSQL = strings.Replace(cachedSelectSQL, "FROM events", "FROM events NOT INDEXED", 1) + `
WHERE rowid > ? AND type = ? AND timestamp_ns >= ?`

// changedRowsSQL lists the rows logged as changed after a log position.
const changedRowsSQL = `SELECT DISTINCT row FROM events_changes WHERE seq > ?`

// changedEventsSQL re-reads the logged rows at or below a rowid (the rows
// above it are read again anyway), each by rowid: NOT INDEXED as in
// newEventsSQL.
var changedEventsSQL = strings.Replace(cachedSelectSQL, "FROM events", "FROM events NOT INDEXED", 1) + `
WHERE rowid IN (SELECT row FROM events_changes WHERE seq > ?) AND rowid <= ? AND type = ? AND timestamp_ns >= ?`

// update applies to the window what changed in the store since its
// snapshot: rows logged as updated or deleted are dropped and read again
// as they are now, and every row past the newest the snapshot still
// knows is read again, which takes in the rows stored since. It reports
// false, changing nothing, when it cannot tell what changed and the
// window must be read in full.
func (ce *cachedEvents) update(ctx context.Context, q querier, t eventschema.EventType, snap snapshot) (bool, error) {
	if snap.seq > ce.seq && (snap.firstSeq == 0 || snap.firstSeq > ce.seq+1) {
		// The log dropped changes this window has not seen.
		return false, nil
	}
	known, ok, err := knownThrough(ctx, q, ce.tail)
	if err != nil || !ok {
		// Rowids were renumbered, or reused past every row the snapshot
		// remembers: they no longer name the events they did.
		return false, err
	}
	var changed map[int64]struct{}
	if snap.seq > ce.seq {
		if changed, err = ce.changedRows(ctx, q); err != nil {
			return false, err
		}
		if len(changed) > len(ce.events) {
			// Re-reading them one by one costs more than the window.
			return false, nil
		}
	}
	events := slices.DeleteFunc(slices.Clone(ce.events), func(e cachedEvent) bool {
		_, logged := changed[e.rowid]
		return logged || e.rowid > known
	})
	if len(changed) > 0 {
		rs, err := q.QueryContext(ctx, changedEventsSQL, ce.seq, known, string(t), ce.floor)
		if err != nil {
			return false, fmt.Errorf("sqlite: event cache: changed events: %w", err)
		}
		reread, err := scanCachedEvents(rs)
		_ = rs.Close()
		if err != nil {
			return false, err
		}
		events = append(events, reread...)
	}
	rs, err := q.QueryContext(ctx, newEventsSQL, known, string(t), ce.floor)
	if err != nil {
		return false, fmt.Errorf("sqlite: event cache: new events: %w", err)
	}
	added, err := scanCachedEvents(rs)
	_ = rs.Close()
	if err != nil {
		return false, err
	}
	events = append(events, added...)
	if !slices.IsSortedFunc(events, compareCached) {
		// Most new rows are the newest events and append in order; a
		// backfill or a re-read row sorts in.
		slices.SortFunc(events, compareCached)
	}
	ce.events = events
	ce.seq, ce.tail = snap.seq, snap.tail
	return true, nil
}

// changedRows is the set of rows logged as changed after ce's position.
func (ce *cachedEvents) changedRows(ctx context.Context, q querier) (map[int64]struct{}, error) {
	rs, err := q.QueryContext(ctx, changedRowsSQL, ce.seq)
	if err != nil {
		return nil, fmt.Errorf("sqlite: event cache: changes: %w", err)
	}
	defer func() { _ = rs.Close() }()
	changed := map[int64]struct{}{}
	for rs.Next() {
		var row int64
		if err := rs.Scan(&row); err != nil {
			return nil, fmt.Errorf("sqlite: event cache: changes: %w", err)
		}
		changed[row] = struct{}{}
	}
	return changed, rs.Err()
}

// trim drops events older than oldest, which no cached read asks for.
func (ce *cachedEvents) trim(oldest int64) {
	if ce.floor >= oldest {
		return
	}
	i := sort.Search(len(ce.events), func(i int) bool { return ce.events[i].env.Timestamp.UnixNano() >= oldest })
	ce.events = slices.Clone(ce.events[i:])
	ce.floor = oldest
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
