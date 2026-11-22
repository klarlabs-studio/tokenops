package sqlite

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// busyTurns appends n prompt events shaped as a busy machine's turns: a
// session every 200 turns, a plan window reading that moves every 20, and
// each turn its own tokens, latency and id.
func busyTurns(t *testing.T, s *Store, n int) {
	t.Helper()
	ctx := context.Background()
	batch := make([]*eventschema.Envelope, 0, 1000)
	for i := 0; i < n; i++ {
		session := fmt.Sprintf("019a%04d-6b1c-7d2e-8f3a-4b5c6d7e8f90", i/200)
		e := &eventschema.Envelope{
			ID: fmt.Sprintf("cxj-%016x", i), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: cacheNow.Add(-time.Duration(n-i) * time.Minute), Source: "codex-jsonl",
			Attributes: map[string]string{
				"session_id": session, "plan_type": "pro", "primary_window_minutes": "300",
				"primary_used_pct": fmt.Sprintf("%d.00", i/20%100), "secondary_used_pct": fmt.Sprintf("%d.00", i/400%100),
			},
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-5.5", ResponseModel: "gpt-5.5",
				InputTokens: int64(40000 + i), OutputTokens: int64(300 + i%700), TotalTokens: int64(40300 + i + i%700),
				CachedInputTokens: int64(39000 + i), ContextSize: int64(41000 + i), Latency: time.Duration(i%9000) * time.Millisecond,
				Status: 200, CostSource: eventschema.CostSourcePlanIncluded, TokenSource: eventschema.TokenSourceCounted,
				WorkflowID: "codex:" + session, AgentID: "codex:tokenops", SessionID: session,
			},
		}
		batch = append(batch, e)
		if len(batch) == cap(batch) || i == n-1 {
			if err := s.AppendBatch(ctx, batch); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
}

// liveHeap is the heap still reachable after a full collection.
func liveHeapBytes() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// The cache holds a month of a busy machine's prompt events (560,000 on
// one machine) for as long as the daemon runs, so what it keeps per event
// is what the daemon keeps: held as decoded envelopes that was 640 bytes
// an event here, and 345 MB of a 500 MB live heap there. Between reads it
// keeps a packed copy, and the envelopes a read handed out last only as
// long as their reader holds them.
func TestEventCacheHoldsPromptEventsPacked(t *testing.T) {
	if testing.Short() {
		t.Skip("appends 20,000 events")
	}
	const n = 20000
	ctx := context.Background()
	s := newTestStore(t)
	busyTurns(t, s, n)
	c := newTestCache(t, s)
	since := cacheNow.Add(-(n + 1) * time.Minute)

	before := liveHeapBytes()
	got, err := c.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil || len(got) != n {
		t.Fatalf("%d events, %v; want %d", len(got), err, n)
	}
	reading := liveHeapBytes()
	runtime.KeepAlive(got)
	held := liveHeapBytes()
	runtime.KeepAlive(c)

	perEvent := func(b uint64) int64 { return (int64(b) - int64(before)) / n }
	t.Logf("per event: %d bytes while read, %d bytes held", perEvent(reading), perEvent(held))
	if got := perEvent(held); got > 240 {
		t.Errorf("the cache holds %d bytes per prompt event between reads; want at most 240", got)
	}
	if got := perEvent(reading); got > 900 {
		t.Errorf("a read holds %d bytes per prompt event; want at most 900", got)
	}

	again, err := c.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil || len(again) != n {
		t.Fatalf("after the envelopes were let go: %d events, %v; want %d", len(again), err, n)
	}
	sameAsStore(t, s, c, eventschema.EventTypePrompt, since, "after the envelopes were let go")
}

// filled sets every field of v, a struct, to a value that is not its
// zero, so a field added to the event later is packed or caught here.
func filled(t *testing.T, v reflect.Value, seed int) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(fmt.Sprintf("%s-%d", v.Type().Field(i).Name, seed))
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(1000*seed + i + 1))
		case reflect.Float64:
			f.SetFloat(float64(seed) + 0.25)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Struct:
			filled(t, f, seed)
		default:
			t.Fatalf("field %s of %s: kind %s is not filled; pack it and add it here", v.Type().Field(i).Name, v.Type(), f.Kind())
		}
	}
}

// An event packs and unpacks to exactly the envelope it was, whatever
// fields it sets; an event the cache cannot pack is held as it is.
func TestPackedEventsUnpackAsTheyWere(t *testing.T) {
	in := newInterner()
	prompt := func(seed int) *eventschema.Envelope {
		var p eventschema.PromptEvent
		filled(t, reflect.ValueOf(&p).Elem(), seed)
		e := &eventschema.Envelope{
			ID: fmt.Sprintf("id-%d", seed), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: cacheNow.Add(time.Duration(seed) * time.Second), Source: "src",
			Attributes: map[string]string{"session_id": "s"}, Payload: &p,
		}
		filled(t, reflect.ValueOf(&e.Association).Elem(), seed)
		filled(t, reflect.ValueOf(&e.Correlation).Elem(), seed)
		return e
	}
	big := prompt(3)
	big.Payload.(*eventschema.PromptEvent).ContextSize = 1 << 40
	traced := prompt(4)
	traced.TraceID, traced.SpanID = "trace", "span"
	empty := prompt(5)
	empty.Attributes = map[string]string{}
	none := prompt(6)
	none.Attributes = nil
	noHash := prompt(7)
	noHash.Payload.(*eventschema.PromptEvent).PromptHash = ""
	domain := &eventschema.Envelope{ID: "d", Type: eventschema.EventTypeDomain, Timestamp: cacheNow, Payload: &eventschema.DomainEvent{Kind: "k"}}

	for _, tc := range []struct {
		name   string
		env    *eventschema.Envelope
		asIsOK bool
	}{
		{"every field set", prompt(1), false},
		{"another session", prompt(2), false},
		{"a count past 32 bits", big, true},
		{"a trace", traced, true},
		{"empty attributes", empty, false},
		{"no attributes", none, false},
		{"no prompt hash", noHash, false},
		{"another type", domain, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := cloneEnvelope(t, tc.env)
			in.cached(tc.env)
			p := in.pack(tc.env)
			if asIs := p.heldAsIs() != nil; asIs != tc.asIsOK {
				t.Fatalf("held as it is: %v; want %v", asIs, tc.asIsOK)
			}
			got := p.envelope()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unpacked\n%+v\nwant\n%+v", got, want)
			}
			if p.source() != want.Source {
				t.Errorf("source %q; want %q", p.source(), want.Source)
			}
			if again := p.envelope(); again != got {
				t.Error("a second read while the first envelope is held builds another")
			}
		})
	}
}

// cloneEnvelope copies e deeply enough to compare against after the
// interner shares its strings.
func cloneEnvelope(t *testing.T, e *eventschema.Envelope) *eventschema.Envelope {
	t.Helper()
	c := *e
	if e.Attributes != nil {
		c.Attributes = maps.Clone(e.Attributes)
	}
	switch p := e.Payload.(type) {
	case *eventschema.PromptEvent:
		cp := *p
		c.Payload = &cp
	case *eventschema.DomainEvent:
		cp := *p
		c.Payload = &cp
	}
	return &c
}

// A scan hands over what ReadEvents returns, in its order or newest first,
// stops when asked, and builds no envelope per event.
func TestEventCacheScansWhatItReads(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	busyTurns(t, s, 3000)
	c := newTestCache(t, s)
	since := cacheNow.Add(-2000 * time.Minute)
	want, err := c.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil {
		t.Fatal(err)
	}
	for _, newestFirst := range []bool{false, true} {
		var got []*eventschema.Envelope
		err := c.ScanEvents(ctx, eventschema.EventTypePrompt, since, newestFirst, func(e *eventschema.Envelope) bool {
			got = append(got, cloneEnvelope(t, e))
			return true
		})
		if err != nil || len(got) != len(want) {
			t.Fatalf("newest first %v: %d events, %v; want %d", newestFirst, len(got), err, len(want))
		}
		for i := range want {
			w := want[i]
			if newestFirst {
				w = want[len(want)-1-i]
			}
			if !reflect.DeepEqual(got[i], w) {
				t.Fatalf("newest first %v: event %d\n%+v\nwant\n%+v", newestFirst, i, got[i], w)
			}
		}
	}
	n := 0
	if err := c.ScanEvents(ctx, eventschema.EventTypePrompt, since, true, func(*eventschema.Envelope) bool { n++; return n < 3 }); err != nil || n != 3 {
		t.Errorf("a scan asked to stop after 3 saw %d, %v", n, err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_ = c.ScanEvents(ctx, eventschema.EventTypePrompt, since, false, func(*eventschema.Envelope) bool { return true })
	runtime.ReadMemStats(&after)
	if per := int64(after.TotalAlloc-before.TotalAlloc) / int64(len(want)); per > 64 {
		t.Errorf("a scan allocated %d bytes per event; it should build them into one envelope", per)
	}
}
