package claudecodejsonl

import (
	"fmt"
	"testing"
	"time"
)

// The dedup set keeps only message IDs a later scan could still meet as a
// duplicate. An old turn is pruned once its scan is done; a recent one
// stays, so its remaining content blocks are still recognised.
func TestSeenSetPrunesTurnsOlderThanTheHorizon(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }
	cases := []struct {
		name     string
		age      time.Duration
		wantKept bool
	}{
		{"recent turn kept", time.Hour, true},
		{"turn just inside the horizon kept", seenHorizon - time.Minute, true},
		{"turn older than the horizon pruned", seenHorizon + time.Minute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newIncrementalFixture(t)
			f.p.now = func() time.Time { return now }
			f.write(assistantLine("msg_x", ts(tc.age)) + "\n")
			f.scan()
			if got := len(f.published()); got != 1 {
				t.Fatalf("published %d, want 1", got)
			}
			_, kept := f.p.seen["msg_x"]
			if kept != tc.wantKept {
				t.Fatalf("kept = %v, want %v", kept, tc.wantKept)
			}
		})
	}
}

// Claude Code writes one line per content block, and the next block can
// land after a scan. Pruning must not let it through as a second turn.
func TestSeenSetStillDedupesBlocksAcrossScans(t *testing.T) {
	f := newIncrementalFixture(t)
	now := time.Now()
	f.p.now = func() time.Time { return now }
	line := assistantLine("msg_a", now.Add(-time.Minute).Format(time.RFC3339Nano)) + "\n"
	f.write(line)
	f.scan()
	f.appendText(line)
	f.scan()
	if got := len(f.published()); got != 1 {
		t.Fatalf("published %d, want 1", got)
	}
}

// However busy the horizon, the set never grows past its cap; the oldest
// turns go first.
func TestSeenSetIsCapped(t *testing.T) {
	f := newIncrementalFixture(t)
	now := time.Now()
	f.p.now = func() time.Time { return now }
	f.p.maxSeen = 3
	var body string
	for i := range 5 {
		body += assistantLine(fmt.Sprintf("msg_%d", i), now.Add(time.Duration(i-10)*time.Minute).Format(time.RFC3339Nano)) + "\n"
	}
	f.write(body)
	f.scan()
	if got := len(f.published()); got != 5 {
		t.Fatalf("published %d, want 5", got)
	}
	if len(f.p.seen) != 3 {
		t.Fatalf("seen holds %d IDs, want the cap of 3", len(f.p.seen))
	}
	for _, id := range []string{"msg_2", "msg_3", "msg_4"} {
		if _, ok := f.p.seen[id]; !ok {
			t.Errorf("newest ID %s evicted", id)
		}
	}
}
