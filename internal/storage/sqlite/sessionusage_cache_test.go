package sqlite

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// sessionTurn is a prompt event of session, with every field
// SessionUsage reads set from rng.
func sessionTurn(id string, at time.Time, session string, rng *rand.Rand) *eventschema.Envelope {
	e := promptAt(id, at, eventschema.ProviderAnthropic, rng.Int63n(5000)+1)
	p := e.Payload.(*eventschema.PromptEvent)
	p.SessionID = session
	p.CachedInputTokens = rng.Int63n(p.InputTokens + 1)
	p.CacheWriteInputTokens = rng.Int63n(100)
	p.CacheWrite1hInputTokens = rng.Int63n(p.CacheWriteInputTokens + 1)
	p.Latency = time.Duration(rng.Int63n(int64(time.Minute)))
	p.WorkflowID = fmt.Sprintf("wf-%d", rng.Intn(4))
	switch rng.Intn(4) {
	case 0:
		p.CostSource = eventschema.CostSourcePlanIncluded
	case 1:
		p.CostUSD = float64(rng.Intn(1000)) / 100
	}
	if rng.Intn(3) == 0 {
		p.RequestModel = "gpt-5"
		p.Provider = eventschema.ProviderOpenAI
	}
	e.Association.Execution = fmt.Sprintf("ex-%d", rng.Intn(3))
	if rng.Intn(2) == 0 {
		e.Attributes["endpoint"] = "api.example"
	}
	return e
}

// seedSessions stores n events over the last span: session turns, some at
// the same instant, with turns outside a session, readings without usage
// and turns of an excluded source among them.
func seedSessions(t testing.TB, s *Store, prefix string, n int, span time.Duration) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(n)))
	envs := make([]*eventschema.Envelope, 0, n)
	for i := range n {
		at := cacheNow.Add(-time.Duration(rng.Int63n(int64(span)))).Truncate(time.Minute)
		e := sessionTurn(fmt.Sprintf("%s%05d", prefix, i), at, fmt.Sprintf("s%d", rng.Intn(50)), rng)
		switch rng.Intn(10) {
		case 0: // outside a session
			e.Payload.(*eventschema.PromptEvent).SessionID = ""
		case 1: // a plan-window reading: no model, no tokens
			p := e.Payload.(*eventschema.PromptEvent)
			p.RequestModel, p.InputTokens, p.OutputTokens = "", 0, 0
			p.CachedInputTokens, p.CacheWriteInputTokens, p.CacheWrite1hInputTokens = 0, 0, 0
		case 2:
			e.Source = "mcp-session"
		}
		envs = append(envs, e)
	}
	if err := s.AppendBatch(context.Background(), envs); err != nil {
		t.Fatal(err)
	}
}

func newTestSessionCache(t *testing.T, s *Store) *SessionUsageCache {
	t.Helper()
	c := NewSessionUsageCache(s, 0, time.Hour)
	c.now = func() time.Time { return cacheNow }
	return c
}

// namedFilter is a read a test checks.
type namedFilter struct {
	name string
	f    analytics.Filter
}

// sessionFilters are the reads the cache answers, the widest first, and
// those it hands to the store, that each step checks.
func sessionFilters() []namedFilter {
	month := cacheNow.Add(-30 * 24 * time.Hour)
	return []namedFilter{
		{"month", analytics.Filter{Since: month}},
		{"two weeks", analytics.Filter{Since: cacheNow.Add(-14 * 24 * time.Hour)}},
		{"last hour", analytics.Filter{Since: cacheNow.Add(-time.Hour)}},
		{"a day, closed", analytics.Filter{Since: cacheNow.Add(-3 * 24 * time.Hour), Until: cacheNow.Add(-2 * 24 * time.Hour)}},
		{"every source", analytics.Filter{Since: month, ExcludeSources: []string{}}},
		{"a source readmitted", analytics.Filter{Since: month, IncludeSources: []string{"mcp-session"}}},
		{"another source left out", analytics.Filter{Since: month, ExcludeSources: []string{"claude-code-jsonl"}}},
		{"prompt type named", analytics.Filter{Since: month, EventType: eventschema.EventTypePrompt}},
		// Handed to the store.
		{"by provider", analytics.Filter{Since: month, Provider: "openai"}},
		{"all time", analytics.Filter{}},
		{"beyond the span", analytics.Filter{Since: cacheNow.Add(-60 * 24 * time.Hour)}},
		{"empty source left", analytics.Filter{Since: month, ExcludeSources: []string{""}}},
	}
}

// sameSessionUsage fails unless the cache answers each of sessionFilters
// exactly as the store does, order included.
func sameSessionUsage(t *testing.T, s *Store, c *SessionUsageCache, step string) {
	t.Helper()
	ctx := context.Background()
	for _, nf := range sessionFilters() {
		name, f := nf.name, nf.f
		want, err := s.SessionUsage(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.SessionUsage(ctx, f)
		if err != nil {
			t.Fatalf("%s, %s: cache: %v", step, name, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s, %s: cache has %d turns, store %d", step, name, len(got), len(want))
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("%s, %s: turn %d differs:\ncache %+v\nstore %+v", step, name, i, got[i], want[i])
			}
		}
	}
}

func sessionFullReads(c *SessionUsageCache) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fullReads
}

// Each kind of change the store sees is applied to the cached window
// without reading it again, and the cache answers what the store does;
// a VACUUM, which may renumber rowids, at most reads it once more.
func TestSessionUsageCacheMatchesTheStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 1500, 40*24*time.Hour)
	c := newTestSessionCache(t, s)
	sameSessionUsage(t, s, c, "first read")
	rng := rand.New(rand.NewSource(7))
	twoWeeks := cacheNow.Add(-14 * 24 * time.Hour)
	steps := []struct {
		name string
		do   func()
		// reread is whether the step may make the cache read its window
		// again.
		reread bool
	}{
		{name: "nothing changed", do: func() {}},
		{name: "insert", do: func() {
			if err := s.Append(ctx, sessionTurn("new-1", cacheNow.Add(-time.Minute), "s1", rng)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "inserts at the same instant as stored turns", do: func() {
			at := cacheNow.Add(-2 * time.Hour).Truncate(time.Minute)
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				sessionTurn("tie-1", at, "s2", rng), sessionTurn("tie-2", at, "s3", rng), sessionTurn("tie-0", at, "s2", rng),
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "backfill", do: func() {
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				sessionTurn("old-1", cacheNow.Add(-20*24*time.Hour), "s4", rng),
				sessionTurn("old-2", cacheNow.Add(-38*24*time.Hour), "s4", rng), // below the window
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "direct update", do: func() {
			execAll(t, s, `UPDATE events SET payload = json_set(payload, '$.cached_input_tokens', 1, '$.latency_ns', 5), input_tokens = 3
				WHERE id IN ('e00003', 'e00100', 'new-1')`)
		}},
		{name: "re-attribution", do: func() {
			if _, err := s.Reattribute(ctx, "claude-code-jsonl", "claude-opus-5", "anthropic", "openrouter", "openrouter.ai", twoWeeks, cacheNow, true); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "restamp", do: func() {
			if _, err := s.RestampPlanIncluded(ctx, "openrouter", cacheNow.Add(-30*24*time.Hour), cacheNow, nil); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a turn leaves its session and another joins one", do: func() {
			execAll(t, s, `UPDATE events SET session_id = NULL WHERE id = 'e00010'`,
				`UPDATE events SET session_id = 'joined' WHERE id IN (SELECT id FROM events WHERE COALESCE(session_id, '') = '' LIMIT 2)`)
		}},
		{name: "a reading gains usage", do: func() {
			execAll(t, s, `UPDATE events SET model = 'claude-opus-5', session_id = 's9'
				WHERE id IN (SELECT id FROM events WHERE COALESCE(model, '') = '' LIMIT 1)`)
		}},
		{name: "a turn moved out of the window and one into it", do: func() {
			execAll(t, s,
				fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'e00007'`, cacheNow.Add(-36*24*time.Hour).UnixNano()),
				fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'old-2'`, cacheNow.Add(-2*time.Hour).UnixNano()))
		}},
		{name: "deletes", do: func() {
			execAll(t, s, `DELETE FROM events WHERE id IN ('e00005', 'e00200', 'old-1')`)
		}},
		{name: "a row given a new rowid", do: func() {
			execAll(t, s, `UPDATE events SET rowid = rowid + 100000 WHERE id = 'e00011'`,
				`UPDATE events SET rowid = 3000 WHERE id = 'e00012'`)
		}},
		{name: "the newest row deleted and its rowid reused", do: func() {
			execAll(t, s, `DELETE FROM events WHERE rowid = (SELECT MAX(rowid) FROM events)`)
			if err := s.Append(ctx, sessionTurn("reuse-1", cacheNow.Add(-3*time.Hour), "s5", rng)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the newest rows deleted and their rowids reused", do: func() {
			execAll(t, s, `DELETE FROM events WHERE rowid IN (SELECT rowid FROM events ORDER BY rowid DESC LIMIT 3)`)
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				sessionTurn("reuse-2", cacheNow.Add(-4*time.Hour), "s6", rng),
				sessionTurn("reuse-3", cacheNow.Add(-25*24*time.Hour), "s6", rng),
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "vacuum after deletes", reread: true, do: func() {
			execAll(t, s, `DELETE FROM events WHERE rowid % 9 = 0`, `VACUUM`)
		}},
		{name: "insert after all of it", reread: true, do: func() {
			if err := s.Append(ctx, sessionTurn("new-2", cacheNow, "s1", rng)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, st := range steps {
		st.do()
		sameSessionUsage(t, s, c, st.name)
		if n := sessionFullReads(c); n != 1 && (!st.reread || n != 2) {
			t.Fatalf("%s: the window was read in full %d times", st.name, n)
		}
	}
}

// Rowids renumbered without a trigger firing, as a VACUUM may: the cache
// cannot trust them, reads its window again, and still answers what the
// store does.
func TestSessionUsageCacheAfterRowidsAreRenumbered(t *testing.T) {
	s := newTestStore(t)
	seedSessions(t, s, "e", 400, 20*24*time.Hour)
	c := newTestSessionCache(t, s)
	sameSessionUsage(t, s, c, "first read")
	execAll(t, s, `DELETE FROM events WHERE rowid % 7 = 0`)
	sameSessionUsage(t, s, c, "after deletes")
	execAll(t, s,
		`DROP TRIGGER events_changes_on_update`,
		`DROP TRIGGER events_changes_on_delete`,
		`CREATE TEMP TABLE renumber AS SELECT * FROM events ORDER BY rowid`,
		`DELETE FROM events`,
		`INSERT INTO events SELECT * FROM renumber`,
		`DROP TABLE renumber`)
	sameSessionUsage(t, s, c, "after renumbering")
	if n := sessionFullReads(c); n != 2 {
		t.Fatalf("renumbered rowids gave %d full reads, want a second", n)
	}
}

// A warm read with nothing changed reads nothing in full, and neither does
// one that only reaches less far back.
func TestSessionUsageCacheWarmReadIsIncremental(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 300, 30*24*time.Hour)
	c := newTestSessionCache(t, s)
	since := cacheNow.Add(-29 * 24 * time.Hour)
	for i := range 3 {
		if _, err := c.SessionUsage(ctx, analytics.Filter{Since: since}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.SessionUsage(ctx, analytics.Filter{Since: since.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if n := sessionFullReads(c); n != 1 {
		t.Fatalf("warm reads with nothing changed read the window %d times, want once", n)
	}
	// A read reaching further back than the window reads it again.
	if _, err := c.SessionUsage(ctx, analytics.Filter{Since: since.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if n := sessionFullReads(c); n != 2 {
		t.Fatalf("a read reaching further back read the window %d times in all, want twice", n)
	}
}

// Reads the cache does not answer go to the store and leave it empty.
func TestSessionUsageCacheLeavesOtherReadsToTheStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 100, 10*24*time.Hour)
	c := newTestSessionCache(t, s)
	for name, f := range map[string]analytics.Filter{
		"by provider":     {Since: cacheNow.Add(-time.Hour), Provider: "openai"},
		"by workflow":     {Since: cacheNow.Add(-time.Hour), WorkflowID: "wf-1"},
		"by agent":        {Since: cacheNow.Add(-time.Hour), AgentID: "a"},
		"by model":        {Since: cacheNow.Add(-time.Hour), Model: "gpt-5"},
		"another type":    {Since: cacheNow.Add(-time.Hour), EventType: eventschema.EventTypeWorkflow},
		"all time":        {},
		"beyond the span": {Since: cacheNow.Add(-50 * 24 * time.Hour)},
	} {
		if _, err := c.SessionUsage(ctx, f); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c.mu.Lock()
		held := len(c.windows)
		c.mu.Unlock()
		if held != 0 {
			t.Fatalf("%s: the cache held a window for a read it does not answer", name)
		}
	}
}

// The cache empties after its idle time.
func TestSessionUsageCacheEmptiesWhenIdle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 50, 5*24*time.Hour)
	c := NewSessionUsageCache(s, 0, 20*time.Millisecond)
	c.now = func() time.Time { return cacheNow }
	if _, err := c.SessionUsage(ctx, analytics.Filter{Since: cacheNow.Add(-24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		held := len(c.windows)
		c.mu.Unlock()
		if held == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the cache kept its rows past its idle time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A cached row shares its session's strings with the other turns of the
// session.
func TestSessionUsageCacheSharesSessionStrings(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rng := rand.New(rand.NewSource(1))
	at := cacheNow.Add(-time.Hour)
	a, b := sessionTurn("a", at, "s1", rng), sessionTurn("b", at.Add(time.Second), "s1", rng)
	b.Payload = a.Payload
	b.Attributes = a.Attributes
	b.Association = a.Association
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{a, b}); err != nil {
		t.Fatal(err)
	}
	c := newTestSessionCache(t, s)
	if _, err := c.SessionUsage(ctx, analytics.Filter{Since: cacheNow.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := c.windows[struct{}{}].rows
	if len(rows) != 2 || rows[0].key != rows[1].key {
		t.Fatalf("two turns of one session hold %d keys", len(rows))
	}
}

// The warm read of a month of session usage, against reading it from the
// store as before.
func BenchmarkSessionUsage(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/events.db", Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	seedSessions(b, s, "e", 30_000, 30*24*time.Hour)
	f := analytics.Filter{Since: cacheNow.Add(-29 * 24 * time.Hour)}
	b.Run("store", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.SessionUsage(ctx, f); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cache-warm", func(b *testing.B) {
		c := NewSessionUsageCache(s, 0, time.Hour)
		c.now = func() time.Time { return cacheNow }
		if _, err := c.SessionUsage(ctx, f); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := c.SessionUsage(ctx, f); err != nil {
				b.Fatal(err)
			}
		}
	})
}
