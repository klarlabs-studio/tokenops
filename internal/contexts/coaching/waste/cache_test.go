package waste

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// warm is a turn that read most of a 100k prefix from the cache.
func warm(at time.Time) CacheTurn {
	return CacheTurn{At: at, Model: "claude-opus-4-7", Input: 100_000, CacheRead: 98_000, CacheWrite: 2_000}
}

// cold is the next turn writing the whole prefix again.
func cold(at time.Time) CacheTurn {
	return CacheTurn{At: at, Model: "claude-opus-4-7", Input: 101_000, CacheRead: 0, CacheWrite: 101_000}
}

func TestClassifyRebuild(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur CacheTurn
		ttl       time.Duration
		want      RebuildCause
		rebuild   bool
	}{
		{name: "idle gap past the 5-minute lifetime", prev: warm(t0), cur: cold(t0.Add(6 * time.Minute)), ttl: CacheTTL, want: CauseTTLExpired, rebuild: true},
		{name: "idle gap past the 1-hour lifetime", prev: warm(t0), cur: cold(t0.Add(61 * time.Minute)), ttl: CacheTTLExtended, want: CauseTTLExpired, rebuild: true},
		{name: "a 6-minute gap inside the 1-hour lifetime", prev: warm(t0), cur: cold(t0.Add(6 * time.Minute)), ttl: CacheTTLExtended, want: CausePrefixChanged, rebuild: true},
		{name: "exactly the lifetime has not expired", prev: warm(t0), cur: cold(t0.Add(CacheTTL)), ttl: CacheTTL, want: CausePrefixChanged, rebuild: true},
		// A 3-minute pause and a 3-minute generation: the request went out
		// three minutes after the turn before, inside the lifetime.
		{name: "the gap runs to when the request went out", prev: warm(t0), cur: func() CacheTurn { c := cold(t0.Add(6 * time.Minute)); c.Latency = 3 * time.Minute; return c }(), ttl: CacheTTL, want: CausePrefixChanged, rebuild: true},
		{name: "without a latency the recorded gap counts", prev: warm(t0), cur: cold(t0.Add(6 * time.Minute)), ttl: CacheTTL, want: CauseTTLExpired, rebuild: true},
		{name: "a latency longer than the gap is not trusted", prev: warm(t0), cur: func() CacheTurn { c := cold(t0.Add(6 * time.Minute)); c.Latency = 7 * time.Minute; return c }(), ttl: CacheTTL, want: CauseTTLExpired, rebuild: true},
		{name: "model switch", prev: warm(t0), cur: func() CacheTurn { c := cold(t0.Add(time.Minute)); c.Model = "claude-sonnet-4-6"; return c }(), ttl: CacheTTL, want: CauseModelSwitch, rebuild: true},
		{name: "model switch wins over an idle gap", prev: warm(t0), cur: func() CacheTurn { c := cold(t0.Add(2 * time.Hour)); c.Model = "claude-sonnet-4-6"; return c }(), ttl: CacheTTL, want: CauseModelSwitch, rebuild: true},
		{name: "compaction: the context fell under half", prev: warm(t0), cur: CacheTurn{At: t0.Add(time.Minute), Model: "claude-opus-4-7", Input: 30_000, CacheRead: 20_000, CacheWrite: 10_000}, ttl: CacheTTL, want: CauseCompaction, rebuild: true},
		{name: "prefix changed after earlier hits", prev: warm(t0), cur: cold(t0.Add(time.Minute)), ttl: CacheTTL, want: CausePrefixChanged, rebuild: true},
		{name: "unknown: the turn before had no hits", prev: CacheTurn{At: t0, Model: "claude-opus-4-7", Input: 100_000, CacheWrite: 100_000}, cur: cold(t0.Add(time.Minute)), ttl: CacheTTL, want: CauseUnknown, rebuild: true},
		{name: "unknown: no times recorded", prev: func() CacheTurn { w := warm(t0); w.At = time.Time{}; return w }(), cur: cold(time.Time{}), ttl: CacheTTL, want: CauseUnknown, rebuild: true},
		{name: "a hit is no rebuild", prev: warm(t0), cur: warm(t0.Add(time.Minute)), ttl: CacheTTL},
		{name: "a small write is no rebuild", prev: warm(t0), cur: CacheTurn{At: t0.Add(10 * time.Minute), Model: "claude-opus-4-7", Input: 101_000, CacheRead: 60_000, CacheWrite: 41_000}, ttl: CacheTTL},
		{name: "nothing cached before", prev: CacheTurn{At: t0, Input: 100_000}, cur: cold(t0.Add(time.Minute)), ttl: CacheTTL},
		{name: "no write", prev: warm(t0), cur: CacheTurn{At: t0.Add(time.Hour), Input: 100_000}, ttl: CacheTTL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ClassifyRebuild(c.prev, c.cur, c.ttl)
			if ok != c.rebuild {
				t.Fatalf("rebuild = %v, want %v (%+v)", ok, c.rebuild, got)
			}
			if ok && got.Cause != c.want {
				t.Errorf("cause = %s, want %s", got.Cause, c.want)
			}
			if ok && got.Tokens <= 0 {
				t.Errorf("a rebuild of %d tokens", got.Tokens)
			}
		})
	}
}

// A compaction's new cache is new by design; every other cause could
// have been avoided.
func TestRebuildCauseAvoidable(t *testing.T) {
	for _, c := range RebuildCauses() {
		if got, want := c.Avoidable(), c != CauseCompaction; got != want {
			t.Errorf("%s avoidable = %v", c, got)
		}
		if RebuildCauseName(c) == string(c) {
			t.Errorf("%s has no name", c)
		}
	}
}

func TestTTLOf(t *testing.T) {
	cases := []struct {
		name  string
		turn  CacheTurn
		ttl   time.Duration
		wrote bool
	}{
		{"five-minute write", CacheTurn{CacheWrite: 500}, CacheTTL, true},
		{"one-hour write", CacheTurn{CacheWrite: 500, CacheWrite1h: 500}, CacheTTLExtended, true},
		{"read only", CacheTurn{CacheRead: 500}, 0, false},
	}
	for _, c := range cases {
		ttl, wrote := TTLOf(c.turn)
		if ttl != c.ttl || wrote != c.wrote {
			t.Errorf("%s: TTLOf = %v, %v; want %v, %v", c.name, ttl, wrote, c.ttl, c.wrote)
		}
	}
}

func TestColdClock(t *testing.T) {
	turns := make([]CacheTurn, 0, 3)
	turns = append(turns,
		CacheTurn{At: t0, CacheWrite: 1_000, CacheWrite1h: 1_000},
		CacheTurn{At: t0.Add(2 * time.Minute), Latency: 30 * time.Second, CacheRead: 1_000},
	)
	last, ttl, ok := ColdClock(turns)
	// The last request went out when the turn began, not when it ended.
	if !ok || !last.Equal(t0.Add(90*time.Second)) || ttl != CacheTTLExtended {
		t.Fatalf("ColdClock = %v, %v, %v", last, ttl, ok)
	}
	// A later five-minute write sets the lifetime again.
	turns = append(turns, CacheTurn{At: t0.Add(3 * time.Minute), CacheWrite: 10})
	if _, ttl, _ := ColdClock(turns); ttl != CacheTTL {
		t.Errorf("ttl after a five-minute write = %v", ttl)
	}
	if _, _, ok := ColdClock([]CacheTurn{{At: t0, Input: 10}}); ok {
		t.Error("a clock for a session that never used the cache")
	}
	// Reads alone: the lifetime is unknown, so the shorter one is assumed.
	if _, ttl, ok := ColdClock([]CacheTurn{{At: t0, CacheRead: 10}}); !ok || ttl != CacheTTL {
		t.Errorf("reads only = %v, %v", ttl, ok)
	}
}

func TestCountdownBoundaries(t *testing.T) {
	last := t0
	cases := []struct {
		after time.Duration
		state CacheState
		left  int64
	}{
		{0, CacheWarm, 300},
		{3 * time.Minute, CacheWarm, 120},                 // exactly two minutes left: still warm
		{3*time.Minute + time.Second, CacheExpiring, 119}, // under two minutes
		{5*time.Minute - time.Second, CacheExpiring, 1},   // a second left
		{5 * time.Minute, CacheCold, 0},                   // at expiry
		{5*time.Minute + time.Hour, CacheCold, 0},         // long gone
		{-time.Minute, CacheWarm, 360},                    // clock skew: the request looks ahead of now
	}
	for _, c := range cases {
		got := Countdown(last, CacheTTL, last.Add(c.after))
		if got.State != c.state || got.RemainingSeconds != c.left {
			t.Errorf("after %v: %s, %ds left; want %s, %ds", c.after, got.State, got.RemainingSeconds, c.state, c.left)
		}
		if !got.ExpiresAt.Equal(last.Add(CacheTTL)) || got.TTLSeconds != 300 || !got.LastRequestAt.Equal(last) {
			t.Errorf("after %v: %+v", c.after, got)
		}
	}
}
