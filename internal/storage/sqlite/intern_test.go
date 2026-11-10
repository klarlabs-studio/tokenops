package sqlite

import (
	"context"
	"maps"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Events whose kept attributes are the same share one map, whether they
// came in on the full read or on a read of new rows, and whatever
// left-out attributes told them apart; an event whose attributes differ
// keeps its own. Their strings are shared too.
func TestEventCacheSharesRepeatedAttributes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := newTestCache(t, s)
	window := map[string]string{"session_id": "s1", "plan_type": "prolite", "primary_used_pct": "12.50"}
	turn := func(id string, at time.Time, seq string, attrs map[string]string) {
		t.Helper()
		e := promptAt(id, at, "openai", 100)
		e.Attributes = maps.Clone(attrs)
		e.Attributes["sequence"] = seq
		e.Payload.(*eventschema.PromptEvent).SessionID = "s1"
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	moved := maps.Clone(window)
	moved["primary_used_pct"] = "13.00"
	turn("a", cacheNow.Add(-3*time.Hour), "1", window)
	turn("b", cacheNow.Add(-2*time.Hour), "2", window)
	since := cacheNow.Add(-24 * time.Hour)
	if _, err := c.ReadEvents(ctx, "prompt", since); err != nil {
		t.Fatal(err)
	}
	turn("c", cacheNow.Add(-time.Hour), "3", window)
	turn("d", cacheNow.Add(-time.Minute), "4", moved)

	got, err := c.ReadEvents(ctx, "prompt", since)
	if err != nil || len(got) != 4 {
		t.Fatalf("%d events, %v; want 4", len(got), err)
	}
	mapOf := func(e *eventschema.Envelope) unsafe.Pointer { return reflect.ValueOf(e.Attributes).UnsafePointer() }
	if mapOf(got[0]) != mapOf(got[1]) || mapOf(got[0]) != mapOf(got[2]) {
		t.Error("turns with the same attributes hold a map each, on the full read or the new rows")
	}
	if mapOf(got[3]) == mapOf(got[0]) {
		t.Error("a turn whose window moved shares the earlier turns' attributes")
	}
	if !maps.Equal(got[3].Attributes, moved) || !maps.Equal(got[0].Attributes, window) {
		t.Errorf("attributes = %v, %v; want %v, %v", got[0].Attributes, got[3].Attributes, window, moved)
	}
	session := func(e *eventschema.Envelope) *byte {
		return unsafe.StringData(e.Payload.(*eventschema.PromptEvent).SessionID)
	}
	if session(got[0]) != session(got[3]) || unsafe.StringData(got[0].Source) != unsafe.StringData(got[3].Source) {
		t.Error("the events hold a copy each of the strings they repeat")
	}
}

// An interner that outgrows its limit starts again, and still answers
// every string and set as given.
func TestInternerStartsAgainWhenFull(t *testing.T) {
	in := newInterner()
	in.held = internLimit
	attrs := map[string]string{"k": "v", "sequence": "1"}
	got := in.attributes(attrs)
	if !maps.Equal(got, map[string]string{"k": "v"}) || in.held != 3 {
		t.Fatalf("after a full interner: %v, holding %d; want the kept attributes, holding 3", got, in.held)
	}
	if again := in.attributes(map[string]string{"k": "v", "sequence": "2"}); reflect.ValueOf(again).UnsafePointer() != reflect.ValueOf(got).UnsafePointer() {
		t.Error("the set handed out after starting again is not shared")
	}
	if in.str("") != "" || in.attributes(nil) != nil {
		t.Error("empty values are not handed back as they are")
	}
}
