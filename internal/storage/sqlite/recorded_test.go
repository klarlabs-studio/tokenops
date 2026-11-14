package sqlite

import (
	"context"
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// windowReading is an event of source carrying a plan window reading and
// the attributes the cache leaves out.
func windowReading(id string, at time.Time, source string, typ eventschema.EventType, rng *rand.Rand) *eventschema.Envelope {
	e := promptAt(id, at, eventschema.ProviderOpenAI, rng.Int63n(500)+1)
	e.Type, e.Source = typ, source
	e.Attributes = map[string]string{
		"primary_used_pct": fmt.Sprint(rng.Intn(100)), "primary_window_min": "300",
		"primary_resets_at": fmt.Sprint(at.Add(time.Hour).Unix()), "session_id": "s",
		"sequence": "7", "message_id": "m", "service_tier": "x",
	}
	if typ != eventschema.EventTypePrompt {
		e.Payload = &eventschema.DomainEvent{Kind: "reading"}
	}
	return e
}

// seen is what fn was called with: each event as it reads, without the
// attributes the cache leaves out.
func seen(t *testing.T, read func(func(*eventschema.Envelope) error) error) []*eventschema.Envelope {
	t.Helper()
	var out []*eventschema.Envelope
	if err := read(func(e *eventschema.Envelope) error {
		c := *e
		c.Attributes = maps.Clone(e.Attributes)
		for _, k := range uncachedAttributes {
			delete(c.Attributes, k)
		}
		out = append(out, &c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// RecordedEvents calls fn with what the store's EachMatching does, in its
// order: a source's prompt events from the cache, its other events from
// the store, through inserts, updates and deletes; and hands every other
// filter to the store.
func TestRecordedEventsMatchesTheStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rng := rand.New(rand.NewSource(3))
	envs := make([]*eventschema.Envelope, 0, 800)
	types := []eventschema.EventType{eventschema.EventTypePrompt, eventschema.EventTypePrompt, eventschema.EventTypePrompt,
		eventschema.EventTypeDomain, eventschema.EventTypeWorkflow}
	for i := range 800 {
		at := cacheNow.Add(-time.Duration(rng.Int63n(int64(35 * 24 * time.Hour)))).Truncate(time.Minute)
		src := []string{"codex-jsonl", "claude-usage-meter", "claude-code-jsonl"}[rng.Intn(3)]
		envs = append(envs, windowReading(fmt.Sprintf("r%04d", i), at, src, types[rng.Intn(len(types))], rng))
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	c := newTestCache(t, s)
	r := NewRecordedEvents(c)
	month := cacheNow.Add(-30 * 24 * time.Hour)
	filters := []Filter{
		{Source: "codex-jsonl", Since: month, Until: cacheNow},
		{Source: "claude-usage-meter", Since: month, Until: cacheNow.Add(-24 * time.Hour)},
		{Source: "codex-jsonl", Since: cacheNow.Add(-2 * 24 * time.Hour)},
		{Source: "nobody", Since: month},
		// Handed to the store.
		{Type: eventschema.EventTypeOutcome, Since: month},
		{Source: "codex-jsonl", Type: eventschema.EventTypeDomain, Since: month},
		{Source: "codex-jsonl"},
		{Source: "codex-jsonl", Since: cacheNow.Add(-60 * 24 * time.Hour)},
	}
	check := func(step string) {
		t.Helper()
		for _, f := range filters {
			want := seen(t, func(fn func(*eventschema.Envelope) error) error { return s.EachMatching(ctx, f, fn) })
			got := seen(t, func(fn func(*eventschema.Envelope) error) error { return r.EachMatching(ctx, f, fn) })
			if len(got) != len(want) {
				t.Fatalf("%s, %+v: %d events, store %d", step, f, len(got), len(want))
			}
			for i := range want {
				if !reflect.DeepEqual(got[i], want[i]) {
					t.Fatalf("%s, %+v: event %d differs:\ngot  %+v\nwant %+v", step, f, i, got[i], want[i])
				}
			}
		}
	}
	check("first read")
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{
		windowReading("new-1", cacheNow.Add(-time.Minute), "codex-jsonl", eventschema.EventTypePrompt, rng),
		windowReading("new-2", cacheNow.Add(-time.Hour), "codex-jsonl", eventschema.EventTypeDomain, rng),
		windowReading("new-3", month.Add(time.Hour), "claude-usage-meter", eventschema.EventTypeWorkflow, rng),
	}); err != nil {
		t.Fatal(err)
	}
	check("after inserts")
	execAll(t, s, `UPDATE events SET source = 'codex-jsonl' WHERE id IN ('r0001', 'r0002', 'r0003')`,
		`UPDATE events SET attributes = json_set(attributes, '$.primary_used_pct', '99') WHERE id IN ('r0010', 'new-1')`,
		`DELETE FROM events WHERE id IN ('r0020', 'r0021')`)
	check("after updates and deletes")
	if n := fullReads(c); n != 1 {
		t.Fatalf("the cache read its window %d times, want once", n)
	}
	// An error from fn ends the read with it.
	stop := fmt.Errorf("stop")
	if err := r.EachMatching(ctx, filters[0], func(*eventschema.Envelope) error { return stop }); err != stop {
		t.Fatalf("fn's error came back as %v", err)
	}
}

// Only a filter by source and time from a start is answered from the
// cache.
func TestBySourceOnly(t *testing.T) {
	since := cacheNow.Add(-time.Hour)
	for _, tc := range []struct {
		f    Filter
		want bool
	}{
		{Filter{Source: "a", Since: since}, true},
		{Filter{Source: "a", Since: since, Until: cacheNow, Limit: 5}, true},
		{Filter{Source: "a"}, false},
		{Filter{Since: since}, false},
		{Filter{Source: "a", Since: since, Type: eventschema.EventTypePrompt}, false},
		{Filter{Source: "a", Since: since, Provider: "openai"}, false},
		{Filter{Source: "a", Since: since, Linked: true}, false},
		{Filter{Source: "a", Since: since, Actors: []string{"x"}}, false},
	} {
		if got := bySourceOnly(tc.f); got != tc.want {
			t.Errorf("bySourceOnly(%+v) = %v, want %v", tc.f, got, tc.want)
		}
	}
}

// A month of one source's readings, read from the store as before and
// through a warm cache.
func BenchmarkRecordedEvents(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/events.db", Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	rng := rand.New(rand.NewSource(1))
	envs := make([]*eventschema.Envelope, 0, 30_000)
	for i := range cap(envs) {
		at := cacheNow.Add(-time.Duration(rng.Int63n(int64(30 * 24 * time.Hour))))
		envs = append(envs, windowReading(fmt.Sprintf("r%05d", i), at, "codex-jsonl", eventschema.EventTypePrompt, rng))
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		b.Fatal(err)
	}
	f := Filter{Source: "codex-jsonl", Since: cacheNow.Add(-29 * 24 * time.Hour), Until: cacheNow}
	count := func(*eventschema.Envelope) error { return nil }
	b.Run("store", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := s.EachMatching(ctx, f, count); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cache-warm", func(b *testing.B) {
		c := NewEventCache(s, 0, time.Hour)
		c.now = func() time.Time { return cacheNow }
		r := NewRecordedEvents(c)
		if err := r.EachMatching(ctx, f, count); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if err := r.EachMatching(ctx, f, count); err != nil {
				b.Fatal(err)
			}
		}
	})
}
