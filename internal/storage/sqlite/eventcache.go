package sqlite

import (
	"context"
	"errors"
	"fmt"
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
// since (by rowid), all inside one read transaction so the answer is one
// snapshot of the store.
//
// An update or delete of a stored event, by any process, bumps
// events_generation (migration 6); a VACUUM may renumber rowids, which
// moves the row the cache last saw. Either makes the next read reload the
// window in full, so the answer is always the one Store.ReadEvents gives.
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
}

// cachedEvents is one event type's window as of a snapshot.
type cachedEvents struct {
	// floor is the earliest timestamp held; every stored event of the
	// type at or after it is in envs.
	floor int64
	// gen is events_generation at the snapshot.
	gen int64
	// lastRowid and lastID are the newest row in the store at the
	// snapshot: rows after it are new, and it must still be there under
	// the same id.
	lastRowid int64
	lastID    string
	// envs is ordered as ReadEvents orders: by timestamp, then id.
	envs []*eventschema.Envelope
}

// Defaults for NewEventCache: a month and a little covers the glance's
// furthest window (the start of the month or two weeks back), and the menu
// bar reads every minute.
const (
	DefaultCacheSpan = 32 * 24 * time.Hour
	DefaultCacheIdle = 10 * time.Minute
)

// NewEventCache caches store's events for windows up to maxSpan back,
// dropping them after idle without a read. Zero values take the defaults.
func NewEventCache(store *Store, maxSpan, idle time.Duration) *EventCache {
	if maxSpan <= 0 {
		maxSpan = DefaultCacheSpan
	}
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
	if errors.Is(err, errNoGeneration) {
		// A store without migration 6 cannot say what changed.
		return c.store.ReadEvents(ctx, t, since)
	}
	if err != nil {
		return nil, err
	}
	i := sort.Search(len(cached.envs), func(i int) bool { return cached.envs[i].Timestamp.UnixNano() >= from })
	out := make([]*eventschema.Envelope, len(cached.envs)-i)
	copy(out, cached.envs[i:])
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

// errNoGeneration marks a store without events_generation.
var errNoGeneration = errors.New("sqlite: store has no events_generation")

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
	cur := c.types[t]
	if cur != nil && cur.floor <= from && cur.gen == snap.gen {
		same, err := rowStill(ctx, tx, cur.lastRowid, cur.lastID)
		if err != nil {
			return nil, err
		}
		if same {
			if err := cur.addSince(ctx, tx, t); err != nil {
				return nil, err
			}
			cur.gen, cur.lastRowid, cur.lastID = snap.gen, snap.lastRowid, snap.lastID
			cur.trim(oldest)
			return cur, nil
		}
	}
	envs, err := readEvents(ctx, tx, t, time.Unix(0, from))
	if err != nil {
		return nil, err
	}
	fresh := &cachedEvents{floor: from, gen: snap.gen, lastRowid: snap.lastRowid, lastID: snap.lastID, envs: envs}
	c.types[t] = fresh
	return fresh, nil
}

// snapshot is what a read transaction saw of the store's changes.
type snapshot struct {
	gen       int64
	lastRowid int64
	lastID    string
}

func snapshotOf(ctx context.Context, q querier) (snapshot, error) {
	var s snapshot
	rows, err := q.QueryContext(ctx, `SELECT gen FROM events_generation WHERE id = 1`)
	if err != nil {
		return s, fmt.Errorf("%w: %w", errNoGeneration, err)
	}
	found := rows.Next()
	if found {
		err = rows.Scan(&s.gen)
	}
	_ = rows.Close()
	if err != nil {
		return s, fmt.Errorf("sqlite: event cache: generation: %w", err)
	}
	if !found {
		return s, errNoGeneration
	}
	rows, err = q.QueryContext(ctx, `SELECT rowid, id FROM events ORDER BY rowid DESC LIMIT 1`)
	if err != nil {
		return s, fmt.Errorf("sqlite: event cache: newest row: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		if err := rows.Scan(&s.lastRowid, &s.lastID); err != nil {
			return s, fmt.Errorf("sqlite: event cache: newest row: %w", err)
		}
	}
	return s, rows.Err()
}

// rowStill reports whether rowid still holds id. An empty store at the
// last snapshot (rowid 0) has nothing to have moved.
func rowStill(ctx context.Context, q querier, rowid int64, id string) (bool, error) {
	if rowid == 0 {
		return true, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM events WHERE rowid = ?`, rowid)
	if err != nil {
		return false, fmt.Errorf("sqlite: event cache: check row: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, rows.Err()
	}
	var got string
	if err := rows.Scan(&got); err != nil {
		return false, fmt.Errorf("sqlite: event cache: check row: %w", err)
	}
	return got == id, nil
}

// newEventsSQL reads the rows stored after a rowid. NOT INDEXED keeps
// SQLite on the rowid seek: left to choose, it walks the (type, timestamp)
// index across the whole window, the read the cache exists to avoid.
var newEventsSQL = strings.Replace(selectSQL, "FROM events", "FROM events NOT INDEXED", 1) + `
WHERE rowid > ? AND type = ? AND timestamp_ns >= ?
ORDER BY timestamp_ns ASC, id ASC`

// addSince merges the rows of type t stored after the last snapshot.
// Most arrive newest, so they append; a backfill of older events sorts
// them in.
func (ce *cachedEvents) addSince(ctx context.Context, q querier, t eventschema.EventType) error {
	rs, err := q.QueryContext(ctx, newEventsSQL, ce.lastRowid, string(t), ce.floor)
	if err != nil {
		return fmt.Errorf("sqlite: event cache: new events: %w", err)
	}
	added, err := scanEnvelopes(rs)
	_ = rs.Close()
	if err != nil || len(added) == 0 {
		return err
	}
	inOrder := len(ce.envs) == 0 || !before(added[0], ce.envs[len(ce.envs)-1])
	ce.envs = append(ce.envs, added...)
	if !inOrder {
		sort.SliceStable(ce.envs, func(i, j int) bool { return before(ce.envs[i], ce.envs[j]) })
	}
	return nil
}

// trim drops events older than oldest, which no cached read asks for.
func (ce *cachedEvents) trim(oldest int64) {
	if ce.floor >= oldest {
		return
	}
	i := sort.Search(len(ce.envs), func(i int) bool { return ce.envs[i].Timestamp.UnixNano() >= oldest })
	ce.envs = append([]*eventschema.Envelope(nil), ce.envs[i:]...)
	ce.floor = oldest
}

// before is ReadEvents' order: timestamp, then id.
func before(a, b *eventschema.Envelope) bool {
	an, bn := a.Timestamp.UnixNano(), b.Timestamp.UnixNano()
	if an != bn {
		return an < bn
	}
	return a.ID < b.ID
}
