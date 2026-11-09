package sqlite

import (
	"context"
	"maps"
	"testing"
	"time"
)

// The cache leaves out the attributes no reader of it asks for, on a full
// read and on a read of new rows alike, and keeps every other one,
// including those readers match by suffix or prefix. The store keeps
// them all.
func TestEventCacheLeavesOutUnreadAttributes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := newTestCache(t, s)
	kept := map[string]string{
		"session": "a", "session_id": "s1", "limit_id": "premium", "plan_type": "prolite",
		"primary_used_pct": "12.5", "primary_resets_at": "1", "secondary_used_pct": "40",
		"weekly_scoped_fable_used_pct": "3", "allowance_0_kind": "reset-credit", "cache_read_input": "9",
	}
	turn := func(id string, at time.Time) {
		t.Helper()
		e := promptAt(id, at, "openai", 100)
		e.Attributes = maps.Clone(kept)
		for _, k := range uncachedAttributes {
			e.Attributes[k] = "x"
		}
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	since := cacheNow.Add(-24 * time.Hour)
	turn("full", cacheNow.Add(-2*time.Hour))
	check := func(step string, want int) {
		t.Helper()
		got, err := c.ReadEvents(ctx, "prompt", since)
		if err != nil || len(got) != want {
			t.Fatalf("%s: %d events, %v; want %d", step, len(got), err, want)
		}
		for _, e := range got {
			if !maps.Equal(e.Attributes, kept) {
				t.Errorf("%s: %s attributes = %v, want %v", step, e.ID, e.Attributes, kept)
			}
		}
	}
	check("full read", 1)
	turn("new", cacheNow.Add(-time.Hour))
	check("new rows", 2)

	stored, err := s.ReadEvents(ctx, "prompt", since)
	if err != nil || len(stored) != 2 || stored[0].Attributes["sequence"] != "x" {
		t.Errorf("the store lost an attribute the cache left out: %v, %v", stored, err)
	}
}
