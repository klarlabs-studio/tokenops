package cache

import (
	"strings"
	"testing"
	"time"
)

func mustEntry(body string) *Entry {
	return &Entry{
		Status:      200,
		Headers:     map[string][]string{"Content-Type": {"application/json"}},
		Body:        []byte(body),
		ContentType: "application/json",
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	c := New(Options{})
	if !c.Put("k1", mustEntry(`{"ok":true}`)) {
		t.Fatal("Put returned false for valid entry")
	}
	got, ok := c.Get("k1")
	if !ok {
		t.Fatal("Get miss after Put")
	}
	if got.Status != 200 || string(got.Body) != `{"ok":true}` {
		t.Errorf("got = %+v", got)
	}
	if c.Metrics().Hits != 1 {
		t.Errorf("hits = %d", c.Metrics().Hits)
	}
}

func TestGetMissIncrementsCounter(t *testing.T) {
	c := New(Options{})
	if _, ok := c.Get("missing"); ok {
		t.Fatal("expected miss")
	}
	if c.Metrics().Misses != 1 {
		t.Errorf("misses = %d", c.Metrics().Misses)
	}
}

func TestTTLEvictsOnAccess(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	c := New(Options{TTL: 100 * time.Millisecond, Now: clock})
	c.Put("k1", mustEntry("hello"))

	now = now.Add(50 * time.Millisecond)
	if _, ok := c.Get("k1"); !ok {
		t.Fatal("entry should still be valid before TTL")
	}

	now = now.Add(200 * time.Millisecond)
	if _, ok := c.Get("k1"); ok {
		t.Fatal("entry should be expired")
	}
	if c.Metrics().Evictions == 0 {
		t.Errorf("expected eviction recorded, got %d", c.Metrics().Evictions)
	}
}

func TestMaxEntriesEvictsLRU(t *testing.T) {
	c := New(Options{MaxEntries: 2})
	c.Put("a", mustEntry("aaaa"))
	c.Put("b", mustEntry("bbbb"))
	// Touch a so b becomes LRU.
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should hit")
	}
	c.Put("c", mustEntry("cccc"))
	if _, ok := c.Get("b"); ok {
		t.Errorf("b should have been evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Errorf("a should still be cached")
	}
}

func TestMaxBytesEvictsLRU(t *testing.T) {
	c := New(Options{MaxBytes: 16, MaxEntryBytes: 16})
	c.Put("a", mustEntry(strings.Repeat("a", 8)))
	c.Put("b", mustEntry(strings.Repeat("b", 8)))
	c.Put("c", mustEntry(strings.Repeat("c", 8))) // forces eviction
	if c.Metrics().Bytes > 16 {
		t.Errorf("bytes over budget: %d", c.Metrics().Bytes)
	}
	if _, ok := c.Get("a"); ok {
		t.Errorf("a should have been evicted")
	}
}

func TestMaxEntryBytesRefuses(t *testing.T) {
	c := New(Options{MaxEntryBytes: 4})
	if ok := c.Put("k", mustEntry("too-large-body")); ok {
		t.Errorf("Put should refuse oversize entry")
	}
	if _, ok := c.Get("k"); ok {
		t.Errorf("oversize entry must not be retrievable")
	}
}

func TestPutOverwritesAndKeepsByteAccounting(t *testing.T) {
	c := New(Options{})
	c.Put("k", mustEntry("aaaaaaaa"))
	beforeBytes := c.Metrics().Bytes
	c.Put("k", mustEntry("bb"))
	afterBytes := c.Metrics().Bytes
	if afterBytes >= beforeBytes {
		t.Errorf("byte accounting wrong: before=%d after=%d", beforeBytes, afterBytes)
	}
}

func TestDeleteAndPurge(t *testing.T) {
	c := New(Options{})
	c.Put("a", mustEntry("body-a"))
	c.Put("b", mustEntry("body-b"))
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Errorf("Delete left entry in cache")
	}
	c.Purge()
	if c.Len() != 0 {
		t.Errorf("Purge len = %d", c.Len())
	}
}

func TestMarkBypassCountsOnly(t *testing.T) {
	c := New(Options{})
	c.MarkBypass()
	c.MarkBypass()
	if c.Metrics().Bypasses != 2 {
		t.Errorf("bypasses = %d", c.Metrics().Bypasses)
	}
}

func TestGetReturnsDefensiveCopy(t *testing.T) {
	c := New(Options{})
	c.Put("k", mustEntry("body"))
	first, _ := c.Get("k")
	first.Body[0] = 'X'
	first.Headers["Content-Type"][0] = "text/plain"

	second, _ := c.Get("k")
	if string(second.Body) != "body" {
		t.Errorf("body mutated through caller copy: %q", string(second.Body))
	}
	if second.Headers["Content-Type"][0] != "application/json" {
		t.Errorf("headers mutated through caller copy: %v", second.Headers)
	}
}
