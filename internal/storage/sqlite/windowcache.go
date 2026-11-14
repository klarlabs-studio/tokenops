package sqlite

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// windowCache holds windows of rows read from the events table, one per
// key, and brings each up to date by reading only what changed since its
// last answer: the rows added since (by rowid) and the rows events_changes
// (migration 7) says were updated or deleted since, all inside one read
// transaction so each answer is one snapshot of the store. It is the
// machinery EventCache and SessionUsageCache share; what a row is and how
// it is read is theirs (windowReads).
//
// Rows are found by rowid, which SQLite gives each new row past the
// largest. Deleting the newest rows lets the next insert reuse their
// rowids, and a VACUUM may renumber them (without firing a trigger): so
// the cache remembers the store's newest rows, and treats every row past
// the newest of them still holding its id as unknown and reads it again.
// A VACUUM renumbers in rowid order, closing gaps: a row that kept its
// rowid had no gap below it, so every row below it kept its rowid too.
//
// It reads a window in full again only when it cannot tell what changed:
// the first read, a read reaching further back than it holds, a change
// log that moved on past it, more changes than rows held, or none of the
// newest rows left under their rowid. It holds at most maxSpan of rows per
// key, and drops them all after idle without a read.
type windowCache[K comparable, T windowRow] struct {
	store *Store
	reads windowReads[K, T]
	// maxSpan is the oldest window kept: a read reaching further back goes
	// to the store uncached.
	maxSpan time.Duration
	// idle is how long the rows outlive the last read.
	idle time.Duration
	now  func() time.Time

	mu      sync.Mutex
	windows map[K]*window[T]
	// filling is the full read in flight per key, run outside mu so the
	// reads the cache already covers are not held behind it.
	filling map[K]*fill
	timer   *time.Timer
	// beforeFullRead, when set, runs as a full read starts, outside mu;
	// for tests.
	beforeFullRead func()
	// fullReads counts the windows read in full, for tests.
	fullReads int
}

// windowRow is a row a window holds.
type windowRow interface {
	// rowID is the events row it was read from.
	rowID() int64
	// unixNano is its event's timestamp.
	unixNano() int64
}

// windowReads is how a windowCache reads one key's rows from q, each row
// readied for the cache by in. Every read keeps to rows at or after from.
type windowReads[K comparable, T windowRow] struct {
	// full reads every row.
	full func(ctx context.Context, q querier, in *interner, key K, from int64) ([]T, error)
	// past reads the rows past rowid known. It must keep SQLite on the
	// rowid seek (NOT INDEXED): left to choose, SQLite walks a timestamp
	// index across the whole window, the read the cache exists to avoid.
	past func(ctx context.Context, q querier, in *interner, key K, known, from int64) ([]T, error)
	// changed reads the rows logged as changed after seq, at or below
	// rowid known (the rows above it are read again anyway): changedRowIDs
	// selects them.
	changed func(ctx context.Context, q querier, in *interner, key K, seq, known, from int64) ([]T, error)
	// compare is the order the store answers in, which a window keeps.
	compare func(a, b T) int
}

// changedRowIDs is the condition a windowReads.changed read selects its
// rows by: its first argument the log position, its second the rowid
// known.
const changedRowIDs = `rowid IN (SELECT row FROM events_changes WHERE seq > ?) AND rowid <= ?`

// window is one key's rows as of a snapshot.
type window[T windowRow] struct {
	// floor is the earliest timestamp held; every stored row of the key
	// at or after it is in rows.
	floor int64
	// seq is the last events_changes entry the window reflects.
	seq int64
	// tail is the newest rows in the store at the snapshot, newest first:
	// rows after the newest one still there are new.
	tail []tailRow
	// rows are in the reads' compare order.
	rows []T
	// strs shares the rows' strings; it is the window's, used under the
	// cache's lock once the window is in the cache.
	strs *interner
}

// init readies c to cache store's rows read by reads, for windows up to
// maxSpan back (cut to what the store logs changes for), dropping them
// after idle without a read.
func (c *windowCache[K, T]) init(store *Store, reads windowReads[K, T], maxSpan, idle time.Duration) {
	c.store, c.reads = store, reads
	c.maxSpan, c.idle, c.now = min(maxSpan, maxCacheSpan), idle, time.Now
	c.windows, c.filling = map[K]*window[T]{}, map[K]*fill{}
}

// view calls see with key's rows at or after from, in compare order, as
// one snapshot of the store. see runs under the cache's lock and must
// neither keep nor modify the rows. view reports false, calling nothing,
// for a read the cache does not answer — reaching further back than its
// span, or on a store without a change log — which the caller reads from
// the store.
func (c *windowCache[K, T]) view(ctx context.Context, key K, from int64, see func([]T)) (bool, error) {
	oldest := c.now().Add(-c.maxSpan).UnixNano()
	if from < oldest {
		return false, nil
	}
	for {
		c.mu.Lock()
		c.touch()
		// What the cache holds, brought up to date: a few rows, under the
		// lock.
		if cur := c.windows[key]; cur != nil && cur.floor <= from {
			ok, err := c.update(ctx, key, cur, oldest)
			if err != nil {
				c.mu.Unlock()
				if errors.Is(err, errNoChangeLog) {
					// A store without migration 7 cannot say what changed.
					return false, nil
				}
				return false, err
			}
			if ok {
				see(cur.since(from))
				c.mu.Unlock()
				return true, nil
			}
		}
		// A full read is needed. One per key at a time: wait for the one
		// in flight, then look again.
		if f := c.filling[key]; f != nil {
			c.mu.Unlock()
			select {
			case <-f.done:
				continue
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		f := &fill{done: make(chan struct{})}
		c.filling[key] = f
		hook := c.beforeFullRead
		c.mu.Unlock()

		if hook != nil {
			hook()
		}
		fresh, err := c.fullRead(ctx, key, from)

		c.mu.Lock()
		delete(c.filling, key)
		close(f.done)
		if err != nil {
			c.mu.Unlock()
			if errors.Is(err, errNoChangeLog) {
				return false, nil
			}
			return false, err
		}
		// Reads that ran meanwhile updated the window this replaces; the
		// next read brings this one up to date from its own snapshot.
		c.fullReads++
		c.windows[key] = fresh
		// The countdown this read started may have run out while the
		// full read ran; these rows are held from now.
		c.touch()
		see(fresh.since(from))
		c.mu.Unlock()
		return true, nil
	}
}

// fill is a full read in flight; done closes when it ends.
type fill struct{ done chan struct{} }

// since is the window's rows at or after from, in order. It is the
// window's own slice.
func (w *window[T]) since(from int64) []T {
	i := sort.Search(len(w.rows), func(i int) bool { return w.rows[i].unixNano() >= from })
	return w.rows[i:]
}

// touch restarts the idle countdown. c.mu is held.
func (c *windowCache[K, T]) touch() {
	if c.timer != nil {
		c.timer.Reset(c.idle)
		return
	}
	c.timer = time.AfterFunc(c.idle, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.windows = map[K]*window[T]{}
	})
}

// errNoChangeLog marks a store without events_changes.
var errNoChangeLog = errors.New("sqlite: store has no events_changes")

// update brings cur up to date with the store from what changed since its
// snapshot, and reports whether it could tell what changed; otherwise the
// window must be read in full. c.mu is held.
func (c *windowCache[K, T]) update(ctx context.Context, key K, cur *window[T], oldest int64) (bool, error) {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("sqlite: cache: begin: %w", err)
	}
	// Read-only: rolling back ends the snapshot.
	defer func() { _ = tx.Rollback() }()
	snap, err := snapshotOf(ctx, tx)
	if err != nil {
		return false, err
	}
	updated, err := c.apply(ctx, tx, key, cur, snap)
	if err != nil || !updated {
		return false, err
	}
	cur.trim(oldest)
	return true, nil
}

// fullRead reads key's window from from in one snapshot of the store. It
// takes no lock: it is the slow read, and runs while the cache answers
// what it already holds.
func (c *windowCache[K, T]) fullRead(ctx context.Context, key K, from int64) (*window[T], error) {
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: cache: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	snap, err := snapshotOf(ctx, tx)
	if err != nil {
		return nil, err
	}
	strs := newInterner()
	rows, err := c.reads.full(ctx, tx, strs, key, from)
	if err != nil {
		return nil, err
	}
	return &window[T]{floor: from, seq: snap.seq, tail: snap.tail, rows: rows, strs: strs}, nil
}

// apply applies to w what changed in the store since its snapshot: rows
// logged as updated or deleted are dropped and read again as they are
// now, and every row past the newest the snapshot still knows is read
// again, which takes in the rows stored since. It reports false, changing
// nothing, when it cannot tell what changed and the window must be read
// in full.
func (c *windowCache[K, T]) apply(ctx context.Context, q querier, key K, w *window[T], snap snapshot) (bool, error) {
	if snap.seq > w.seq && (snap.firstSeq == 0 || snap.firstSeq > w.seq+1) {
		// The log dropped changes this window has not seen.
		return false, nil
	}
	known, ok, err := knownThrough(ctx, q, w.tail)
	if err != nil || !ok {
		// Rowids were renumbered, or reused past every row the snapshot
		// remembers: they no longer name the rows they did.
		return false, err
	}
	var changed map[int64]struct{}
	if snap.seq > w.seq {
		if changed, err = changedRows(ctx, q, w.seq); err != nil {
			return false, err
		}
		if len(changed) > len(w.rows) {
			// Re-reading them one by one costs more than the window.
			return false, nil
		}
	}
	stale := func(r T) bool {
		_, logged := changed[r.rowID()]
		return logged || r.rowID() > known
	}
	rows := w.rows
	if slices.ContainsFunc(rows, stale) {
		// On a copy: a failed read below leaves the window as it was.
		rows = slices.DeleteFunc(slices.Clone(rows), stale)
	}
	if len(changed) > 0 {
		reread, err := c.reads.changed(ctx, q, w.strs, key, w.seq, known, w.floor)
		if err != nil {
			return false, err
		}
		rows = append(rows, reread...)
	}
	added, err := c.reads.past(ctx, q, w.strs, key, known, w.floor)
	if err != nil {
		return false, err
	}
	rows = append(rows, added...)
	if !slices.IsSortedFunc(rows, c.reads.compare) {
		// Most new rows are the newest events and append in order; a
		// backfill or a re-read row sorts in.
		slices.SortFunc(rows, c.reads.compare)
	}
	w.rows = rows
	w.seq, w.tail = snap.seq, snap.tail
	return true, nil
}

// trim drops rows older than oldest, which no cached read asks for.
func (w *window[T]) trim(oldest int64) {
	if w.floor >= oldest {
		return
	}
	i := sort.Search(len(w.rows), func(i int) bool { return w.rows[i].unixNano() >= oldest })
	w.rows = slices.Clone(w.rows[i:])
	w.floor = oldest
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
		return s, fmt.Errorf("sqlite: cache: change log: %w", err)
	}
	if rows.Next() {
		err = rows.Scan(&s.firstSeq, &s.seq)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return s, fmt.Errorf("sqlite: cache: change log: %w", err)
	}
	rows, err = q.QueryContext(ctx, `SELECT rowid, id FROM events ORDER BY rowid DESC LIMIT ?`, tailRows)
	if err != nil {
		return s, fmt.Errorf("sqlite: cache: newest rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r tailRow
		if err := rows.Scan(&r.rowid, &r.id); err != nil {
			return s, fmt.Errorf("sqlite: cache: newest rows: %w", err)
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
		return 0, false, fmt.Errorf("sqlite: cache: newest rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	now := make(map[int64]string, len(tail))
	for rows.Next() {
		var r tailRow
		if err := rows.Scan(&r.rowid, &r.id); err != nil {
			return 0, false, fmt.Errorf("sqlite: cache: newest rows: %w", err)
		}
		now[r.rowid] = r.id
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("sqlite: cache: newest rows: %w", err)
	}
	for _, r := range tail {
		if id, there := now[r.rowid]; there && id == r.id {
			return r.rowid, true, nil
		}
	}
	return 0, false, nil
}

// changedRowsSQL lists the rows logged as changed after a log position.
const changedRowsSQL = `SELECT DISTINCT row FROM events_changes WHERE seq > ?`

// changedRows is the set of rows logged as changed after seq.
func changedRows(ctx context.Context, q querier, seq int64) (map[int64]struct{}, error) {
	rs, err := q.QueryContext(ctx, changedRowsSQL, seq)
	if err != nil {
		return nil, fmt.Errorf("sqlite: cache: changes: %w", err)
	}
	defer func() { _ = rs.Close() }()
	changed := map[int64]struct{}{}
	for rs.Next() {
		var row int64
		if err := rs.Scan(&row); err != nil {
			return nil, fmt.Errorf("sqlite: cache: changes: %w", err)
		}
		changed[row] = struct{}{}
	}
	return changed, rs.Err()
}
