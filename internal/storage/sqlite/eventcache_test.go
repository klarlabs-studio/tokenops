package sqlite

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// cacheNow is the real time: the change log's triggers log changes to
// events by how old they are now.
var cacheNow = time.Now().UTC().Truncate(time.Second)

func newTestCache(t *testing.T, s *Store) *EventCache {
	t.Helper()
	c := NewEventCache(s, 32*24*time.Hour, time.Hour)
	c.now = func() time.Time { return cacheNow }
	return c
}

func promptAt(id string, at time.Time, provider eventschema.Provider, tokens int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: at, Source: "claude-code-jsonl",
		Attributes: map[string]string{"session": id},
		Payload: &eventschema.PromptEvent{
			Provider: provider, RequestModel: "claude-opus-5", InputTokens: tokens, OutputTokens: tokens / 10,
			CostSource: eventschema.CostSourceMetered,
		},
	}
}

// sameAsStore fails unless the cache answers exactly what the store does.
func sameAsStore(t *testing.T, s *Store, c *EventCache, typ eventschema.EventType, since time.Time, step string) {
	t.Helper()
	ctx := context.Background()
	want, err := s.ReadEvents(ctx, typ, since)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadEvents(ctx, typ, since)
	if err != nil {
		t.Fatalf("%s: cache: %v", step, err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: cache has %d events since %s, store %d", step, len(got), since, len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("%s: event %d differs:\ncache %+v\nstore %+v", step, i, got[i], want[i])
		}
	}
}

// Every way the store changes — new events, backfilled older ones, other
// event types, re-attribution, a restamp, a retention delete, a VACUUM —
// leaves the cache answering what Store.ReadEvents answers, for windows
// inside, below and beyond what it holds.
func TestEventCacheAnswersAsTheStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := newTestCache(t, s)
	rng := rand.New(rand.NewSource(7))
	seq := 0
	add := func(n int, oldest time.Duration) {
		t.Helper()
		envs := make([]*eventschema.Envelope, 0, n)
		for i := 0; i < n; i++ {
			seq++
			at := cacheNow.Add(-time.Duration(rng.Int63n(int64(oldest))))
			p := eventschema.ProviderAnthropic
			if rng.Intn(3) == 0 {
				p = eventschema.ProviderOpenAI
			}
			envs = append(envs, promptAt(fmt.Sprintf("e%05d", seq), at, p, rng.Int63n(5000)))
		}
		if err := s.AppendBatch(ctx, envs); err != nil {
			t.Fatal(err)
		}
	}
	twoWeeks := cacheNow.Add(-14 * 24 * time.Hour)
	month := cacheNow.Add(-30 * 24 * time.Hour)
	check := func(step string) {
		t.Helper()
		for _, since := range []time.Time{twoWeeks, twoWeeks.Add(time.Hour), cacheNow.Add(-5 * time.Hour), month, cacheNow.Add(-60 * 24 * time.Hour)} {
			sameAsStore(t, s, c, eventschema.EventTypePrompt, since, step)
		}
		sameAsStore(t, s, c, eventschema.EventTypePrompt, twoWeeks, step)
	}

	add(300, 60*24*time.Hour)
	check("first read")
	add(20, time.Hour)
	check("new events")
	add(20, 40*24*time.Hour)
	check("backfilled older events")
	if err := s.Append(ctx, &eventschema.Envelope{
		ID: "domain-1", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeDomain,
		Timestamp: cacheNow, Payload: &eventschema.DomainEvent{Kind: "test", Data: []byte(`{"a":1}`)},
	}); err != nil {
		t.Fatal(err)
	}
	check("another type")
	sameAsStore(t, s, c, eventschema.EventTypeDomain, twoWeeks, "domain events")
	if _, err := s.Reattribute(ctx, "claude-code-jsonl", "claude-opus-5", "anthropic", "openrouter", "openrouter.ai", month, cacheNow, true); err != nil {
		t.Fatal(err)
	}
	check("re-attribution")
	if _, err := s.RestampPlanIncluded(ctx, "openrouter", month, cacheNow); err != nil {
		t.Fatal(err)
	}
	check("restamp")
	add(10, time.Hour)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE type = 'prompt' AND timestamp_ns < ?`, cacheNow.Add(-20*24*time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	check("retention delete")
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		t.Fatal(err)
	}
	add(10, time.Hour)
	check("vacuum")
}

// Without the change log, a row moved under the one the cache last saw (as
// a VACUUM may renumber rowids) still forces a reload.
func TestEventCacheReloadsWhenItsLastRowMoved(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := newTestCache(t, s)
	since := cacheNow.Add(-24 * time.Hour)
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{
		promptAt("a", cacheNow.Add(-time.Hour), eventschema.ProviderAnthropic, 10),
		promptAt("b", cacheNow.Add(-2*time.Hour), eventschema.ProviderAnthropic, 20),
	}); err != nil {
		t.Fatal(err)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, since, "first read")
	for _, q := range []string{
		`DROP TRIGGER events_changes_on_update`,
		`DROP TRIGGER events_changes_on_delete`,
		// Renumber: b takes a rowid past every other, a moves into its old one.
		`UPDATE events SET rowid = 1000 WHERE id = 'b'`,
		`UPDATE events SET rowid = 2 WHERE id = 'a'`,
		`UPDATE events SET payload = json_set(payload, '$.input_tokens', 99) WHERE id = 'a'`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, since, "after renumbering")
}

// A window older than the cache's span is the store's to answer, and the
// cache empties after its idle time.
func TestEventCacheBoundsWhatItHolds(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := NewEventCache(s, 7*24*time.Hour, 20*time.Millisecond)
	c.now = func() time.Time { return cacheNow }
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{
		promptAt("old", cacheNow.Add(-10*24*time.Hour), eventschema.ProviderAnthropic, 10),
		promptAt("new", cacheNow.Add(-time.Hour), eventschema.ProviderAnthropic, 20),
	}); err != nil {
		t.Fatal(err)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-9*24*time.Hour), "beyond the span")
	c.mu.Lock()
	held := len(c.types)
	c.mu.Unlock()
	if held != 0 {
		t.Fatalf("a window beyond the span was cached")
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-2*24*time.Hour), "inside the span")
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		held = len(c.types)
		c.mu.Unlock()
		if held == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cache kept its events past its idle time")
		}
		time.Sleep(5 * time.Millisecond)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, cacheNow.Add(-2*24*time.Hour), "after going idle")
}

// The read of new events seeks on rowid; scanning the window instead
// would cost what the cache exists to save.
func TestEventCacheReadsNewEventsByRowid(t *testing.T) {
	s := newTestStore(t)
	plan := explain(t, s, newEventsSQL,
		[]any{int64(1), "prompt", int64(0)})
	if !strings.Contains(plan, "INTEGER PRIMARY KEY (rowid>?)") {
		t.Fatalf("new events are not read by rowid:\n%s", plan)
	}
	// Changed events too: each logged row is looked up by rowid, and the
	// log is read from the cache's position on.
	plan = explain(t, s, changedEventsSQL, []any{int64(1), int64(100), "prompt", int64(0)})
	if !strings.Contains(plan, "INTEGER PRIMARY KEY (rowid=?)") || !strings.Contains(plan, "SEARCH events_changes USING INTEGER PRIMARY KEY (rowid>?)") {
		t.Fatalf("changed events are not read by rowid:\n%s", plan)
	}
}
