package sqlite

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// seedCache stores n prompt events spread over the last span, named
// prefix0000…, and returns a cache that has read the last month of them.
func seedCache(t *testing.T, s *Store, prefix string, n int, span time.Duration) *EventCache {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(n)))
	envs := make([]*eventschema.Envelope, n)
	for i := range envs {
		at := cacheNow.Add(-time.Duration(rng.Int63n(int64(span))))
		envs[i] = promptAt(fmt.Sprintf("%s%05d", prefix, i), at, eventschema.ProviderAnthropic, rng.Int63n(5000)+1)
	}
	if err := s.AppendBatch(context.Background(), envs); err != nil {
		t.Fatal(err)
	}
	c := newTestCache(t, s)
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-30*24*time.Hour), "first read")
	return c
}

// fullReads is how many windows c has read in full.
func fullReads(c *EventCache) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fullReads
}

// execAll runs each statement on s.
func execAll(t *testing.T, s *Store, stmts ...string) {
	t.Helper()
	for _, q := range stmts {
		if _, err := s.db.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// countChanges is how many entries the change log holds.
func countChanges(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events_changes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Each kind of change the store sees is applied to the cached window
// without reading it again, and the cache answers what the store does.
func TestEventCacheRereadsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := seedCache(t, s, "e", 600, 40*24*time.Hour)
	month := cacheNow.Add(-30 * 24 * time.Hour)
	twoWeeks := cacheNow.Add(-14 * 24 * time.Hour)
	check := func(step string) {
		t.Helper()
		for _, since := range []time.Time{month, twoWeeks, cacheNow.Add(-time.Hour), month.Add(time.Nanosecond)} {
			sameAsStore(t, s, c, eventschema.EventTypePrompt, since, step)
		}
		if n := fullReads(c); n != 1 {
			t.Fatalf("%s: the window was read in full %d times, want once", step, n)
		}
	}
	steps := []struct {
		name string
		do   func()
	}{
		{"insert", func() {
			if err := s.Append(ctx, promptAt("new-1", cacheNow.Add(-time.Minute), eventschema.ProviderOpenAI, 7)); err != nil {
				t.Fatal(err)
			}
		}},
		{"backfill of older events", func() {
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				promptAt("old-1", cacheNow.Add(-20*24*time.Hour), eventschema.ProviderOpenAI, 8),
				promptAt("old-2", cacheNow.Add(-35*24*time.Hour), eventschema.ProviderOpenAI, 9), // below the window
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"update of a few rows", func() {
			execAll(t, s, `UPDATE events SET payload = json_set(payload, '$.input_tokens', 1), input_tokens = 1 WHERE id IN ('e00003', 'e00100', 'new-1')`)
		}},
		{"re-attribution", func() {
			if _, err := s.Reattribute(ctx, "claude-code-jsonl", "claude-opus-5", "anthropic", "openrouter", "openrouter.ai", twoWeeks, cacheNow, true); err != nil {
				t.Fatal(err)
			}
		}},
		{"restamp", func() {
			if _, err := s.RestampPlanIncluded(ctx, "openrouter", month, cacheNow); err != nil {
				t.Fatal(err)
			}
		}},
		{"delete of a few rows", func() {
			execAll(t, s, `DELETE FROM events WHERE id IN ('e00005', 'e00200', 'old-1')`)
		}},
		{"a row moved out of the window and one into it", func() {
			execAll(t, s,
				fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'e00007'`, cacheNow.Add(-36*24*time.Hour).UnixNano()),
				fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'old-2'`, cacheNow.Add(-2*time.Hour).UnixNano()))
		}},
		{"a row changed type", func() {
			execAll(t, s, `UPDATE events SET type = 'workflow' WHERE id = 'e00009'`)
		}},
		{"a row given a new rowid", func() {
			execAll(t, s, `UPDATE events SET rowid = rowid + 100000 WHERE id = 'e00011'`,
				`UPDATE events SET rowid = 3000 WHERE id = 'e00012'`)
		}},
		{"the newest rows deleted and their rowids reused", func() {
			execAll(t, s, `DELETE FROM events WHERE rowid IN (SELECT rowid FROM events ORDER BY rowid DESC LIMIT 3)`)
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				promptAt("reuse-1", cacheNow.Add(-3*time.Hour), eventschema.ProviderOpenAI, 11),
				promptAt("reuse-2", cacheNow.Add(-25*24*time.Hour), eventschema.ProviderOpenAI, 12),
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"insert after all of it", func() {
			if err := s.Append(ctx, promptAt("new-2", cacheNow, eventschema.ProviderOpenAI, 10)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, st := range steps {
		st.do()
		check(st.name)
	}
}

// A retention pass deletes old events, which no cache holds: it logs
// nothing, so a large one costs no second write per row, and the cache
// carries on without reading its window again.
func TestRetentionDeleteLogsNothing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := seedCache(t, s, "e", 2000, 120*24*time.Hour)
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE type = 'prompt' AND timestamp_ns < ?`,
		cacheNow.Add(-changeLogHorizon-24*time.Hour).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n < 1000 {
		t.Fatalf("retention deleted %d rows; the test needs a large delete", n)
	}
	if n := countChanges(t, s); n != 0 {
		t.Fatalf("a delete of events past the horizon logged %d changes", n)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-30*24*time.Hour), "after retention")
	if n := fullReads(c); n != 1 {
		t.Fatalf("retention made the cache read its window again (%d full reads)", n)
	}
	// A delete reaching into the window is logged, row by row.
	res, err = s.db.ExecContext(ctx, `DELETE FROM events WHERE type = 'prompt' AND timestamp_ns >= ? AND timestamp_ns < ?`,
		cacheNow.Add(-35*24*time.Hour).UnixNano(), cacheNow.Add(-20*24*time.Hour).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	deleted, _ := res.RowsAffected()
	if n := countChanges(t, s); int64(n) != deleted {
		t.Fatalf("a delete of %d events younger than the horizon logged %d", deleted, n)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-30*24*time.Hour), "after a delete inside the window")
	if n := fullReads(c); n != 1 {
		t.Fatalf("a delete inside the window made the cache read it again (%d full reads)", n)
	}
}

// The change log keeps its latest entries only; a cache further behind
// than that reads its window again rather than miss a change.
func TestEventCacheReloadsWhenTheLogMovedOn(t *testing.T) {
	s := newTestStore(t)
	c := seedCache(t, s, "e", 100, 20*24*time.Hour)
	execAll(t, s,
		// As if a long run of changes had been logged since the cache's read.
		fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d)
			INSERT INTO events_changes (row) SELECT 0 FROM n`, changeLogKeep+100),
		`UPDATE events SET payload = json_set(payload, '$.input_tokens', 3) WHERE id = 'e00001'`)
	if n := countChanges(t, s); n != changeLogKeep {
		t.Fatalf("the log holds %d entries, want its cap of %d", n, changeLogKeep)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-30*24*time.Hour), "after the log moved on")
	if n := fullReads(c); n != 2 {
		t.Fatalf("a cache behind the log read its window %d times, want a second full read", n)
	}
}

// A VACUUM may renumber rowids without firing a trigger. The cache then
// cannot trust its rowids or the log's, and reads its window again.
func TestEventCacheAfterRowidsAreRenumbered(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := seedCache(t, s, "e", 300, 20*24*time.Hour)
	execAll(t, s, `DELETE FROM events WHERE rowid % 7 = 0`)
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-30*24*time.Hour), "after deletes")

	// Renumber densely as a VACUUM may, with the triggers out of the way
	// as a VACUUM fires none, and log one ordinary change after.
	var triggers []string
	rows, err := s.db.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'events'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			t.Fatal(err)
		}
		triggers = append(triggers, q)
	}
	_ = rows.Close()
	if len(triggers) != 2 {
		t.Fatalf("found %d triggers on events, want 2", len(triggers))
	}
	execAll(t, s,
		`DROP TRIGGER events_changes_on_update`,
		`DROP TRIGGER events_changes_on_delete`,
		`CREATE TEMP TABLE renumber AS SELECT * FROM events ORDER BY rowid`,
		`DELETE FROM events`,
		`INSERT INTO events SELECT * FROM renumber`,
		`DROP TABLE renumber`)
	execAll(t, s, triggers...)
	execAll(t, s, `UPDATE events SET payload = json_set(payload, '$.input_tokens', 5) WHERE id = 'e00010'`)
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-30*24*time.Hour), "after renumbering")
	if n := fullReads(c); n != 2 {
		t.Fatalf("renumbered rowids gave %d full reads, want a second", n)
	}
}

// Changes are logged for events younger than the horizon only, and a row
// given a new rowid is logged under both.
func TestChangeLogTriggers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{
		promptAt("recent", cacheNow.Add(-time.Hour), eventschema.ProviderAnthropic, 1),
		promptAt("old", cacheNow.Add(-changeLogHorizon-time.Hour), eventschema.ProviderAnthropic, 1),
	}); err != nil {
		t.Fatal(err)
	}
	execAll(t, s, `UPDATE events SET input_tokens = 2 WHERE id = 'old'`)
	if n := countChanges(t, s); n != 0 {
		t.Fatalf("an update of an event past the horizon logged %d changes", n)
	}
	execAll(t, s, fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'old'`, cacheNow.UnixNano()))
	if n := countChanges(t, s); n != 1 {
		t.Fatalf("an event moved into the horizon logged %d changes, want 1", n)
	}
	execAll(t, s, `UPDATE events SET rowid = 500 WHERE id = 'recent'`)
	var rows []int64
	rs, err := s.db.QueryContext(ctx, `SELECT row FROM events_changes ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var r int64
		if err := rs.Scan(&r); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	_ = rs.Close()
	if len(rows) != 3 || rows[1] != 500 || rows[2] != 1 {
		t.Fatalf("a new rowid logged %v, want the new and the old after the first entry", rows)
	}
}

// Warm reads the window ahead of the first caller, who then reads nothing
// in full.
func TestEventCacheWarm(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.Append(ctx, promptAt("a", cacheNow.Add(-time.Hour), eventschema.ProviderAnthropic, 1)); err != nil {
		t.Fatal(err)
	}
	c := newTestCache(t, s)
	if err := c.Warm(ctx, eventschema.EventTypePrompt, cacheNow.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-14*24*time.Hour), "after warming")
	if n := fullReads(c); n != 1 {
		t.Fatalf("the first read after warming read the window again (%d full reads)", n)
	}
}

// A span beyond what the store logs changes for is cut to what it does.
func TestEventCacheSpanStaysInsideTheLog(t *testing.T) {
	c := NewEventCache(newTestStore(t), 90*24*time.Hour, 0)
	if c.maxSpan != maxCacheSpan || maxCacheSpan >= changeLogHorizon {
		t.Fatalf("span %v, log horizon %v", c.maxSpan, changeLogHorizon)
	}
}

// Writers in this process and another insert, backfill, update and delete
// while the cache is read; every answer is ordered and whole, and once the
// writers stop the cache answers what the store does.
func TestEventCacheWithConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Another process's connection to the same store.
	other, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	c := seedCache(t, s, "seed-", 500, 30*24*time.Hour)
	month := cacheNow.Add(-30 * 24 * time.Hour)

	var id atomic.Int64
	writer := func(w *Store, seed int64, stop <-chan struct{}) error {
		rng := rand.New(rand.NewSource(seed))
		for {
			select {
			case <-stop:
				return nil
			default:
			}
			var err error
			switch rng.Intn(4) {
			case 0: // new events
				err = w.Append(ctx, promptAt(fmt.Sprintf("w%07d", id.Add(1)), cacheNow.Add(-time.Duration(rng.Int63n(int64(time.Hour)))), eventschema.ProviderOpenAI, rng.Int63n(100)+1))
			case 1: // backfill
				err = w.Append(ctx, promptAt(fmt.Sprintf("w%07d", id.Add(1)), cacheNow.Add(-time.Duration(rng.Int63n(int64(40*24*time.Hour)))), eventschema.ProviderOpenAI, rng.Int63n(100)+1))
			case 2: // update a few
				_, err = w.db.ExecContext(ctx, `UPDATE events SET input_tokens = ?, payload = json_set(payload, '$.input_tokens', ?)
					WHERE rowid IN (SELECT rowid FROM events ORDER BY random() LIMIT 3)`, rng.Int63n(1000)+1, rng.Int63n(1000)+1)
			case 3: // delete one
				_, err = w.db.ExecContext(ctx, `DELETE FROM events WHERE rowid IN (SELECT rowid FROM events ORDER BY random() LIMIT 1)`)
			}
			if err != nil && !IsContended(err) {
				return err
			}
		}
	}
	for round := range 3 {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i, w := range []*Store{s, s, other} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- writer(w, int64(round*10+i), stop)
			}()
		}
		for range 40 {
			got, err := c.ReadEvents(ctx, eventschema.EventTypePrompt, month)
			if err != nil {
				t.Fatal(err)
			}
			seen := make(map[string]bool, len(got))
			for i, e := range got {
				if seen[e.ID] {
					t.Fatalf("round %d: %s answered twice", round, e.ID)
				}
				seen[e.ID] = true
				if i > 0 && compareCached(cachedEvent{env: got[i-1]}, cachedEvent{env: e}) >= 0 {
					t.Fatalf("round %d: answer out of order at %d", round, i)
				}
			}
		}
		close(stop)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		sameAsStore(t, s, c, eventschema.EventTypePrompt, month, fmt.Sprintf("round %d", round))
	}
}
