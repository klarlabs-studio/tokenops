package avoidable

import (
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// cacheTurn is a turn of a 10,000-token prefix with the given cache
// figures, worth $1.
func cacheTurn(session string, at time.Duration, model string, read, write, write1h int64) analytics.SessionTurn {
	return analytics.SessionTurn{SessionID: session, At: t0.Add(at), Provider: "anthropic", Model: model,
		Tokens: 10_100, InputTokens: 10_000, OutputTokens: 100, CacheReadTokens: read, CacheWriteTokens: write,
		CacheWrite1hTokens: write1h, APIEquivalentUSD: 1, Priced: true}
}

const opus = "claude-opus-4-7"

// causesFixture holds one rebuild per cause, each in its own session after
// a turn that read its prefix from the cache.
func causesFixture() Inputs {
	hit := func(s string) analytics.SessionTurn { return cacheTurn(s, 0, opus, 9_500, 500, 0) }
	turns := []analytics.SessionTurn{
		// Idle for ten minutes on a five-minute cache.
		hit("idle"), cacheTurn("idle", 10*time.Minute, opus, 0, 10_000, 0),
		// Idle for ten minutes on an hour-long cache: not expired.
		cacheTurn("hour", 0, opus, 9_500, 500, 500), cacheTurn("hour", 10*time.Minute, opus, 0, 10_000, 10_000),
		// Idle twice past the hour on an hour-long cache.
		cacheTurn("long", 0, opus, 9_500, 500, 500), cacheTurn("long", 2*time.Hour, opus, 0, 10_000, 10_000),
		cacheTurn("long", 4*time.Hour, opus, 0, 10_000, 10_000),
		// Switched model a minute later.
		hit("switch"), cacheTurn("switch", time.Minute, "claude-haiku-4-5", 0, 10_000, 0),
		// Compacted: the next turn's context is a fifth of the last.
		hit("compact"), func() analytics.SessionTurn {
			c := cacheTurn("compact", time.Minute, opus, 0, 2_000, 0)
			c.InputTokens = 2_000
			return c
		}(),
	}
	in := Inputs{Since: t0, Until: t0.Add(7 * day), Turns: turns, Price: price,
		Windows: map[string][]plans.QuotaWindow{"anthropic": {
			{Label: "weekly", UsedPct: 10, ResetsAt: t0.Add(7*day - time.Hour), Duration: 7 * day},
		}}}
	return in
}

func causeOf(cc ClassCost, c waste.RebuildCause) (CauseCost, bool) {
	for _, x := range cc.Causes {
		if x.Cause == c {
			return x, true
		}
	}
	return CauseCost{}, false
}

func TestCacheRebuildsSplitByCause(t *testing.T) {
	r := Build(causesFixture())
	rb := class(r, waste.ClassCacheRebuild)
	want := map[waste.RebuildCause]int{
		waste.CauseTTLExpired:    3, // "idle" and both of "long"'s
		waste.CausePrefixChanged: 1, // "hour": inside the hour, yet rebuilt
	}
	// The haiku turn has a list price, so the switch is priced too.
	want[waste.CauseModelSwitch] = 1
	for c, n := range want {
		got, ok := causeOf(rb, c)
		if !ok || got.Turns != n || got.Tokens <= 0 || got.APIEquivalentUSD <= 0 {
			t.Errorf("%s = %+v, want %d turns", c, got, n)
		}
		if got.Name != waste.RebuildCauseName(c) {
			t.Errorf("%s named %q", c, got.Name)
		}
	}
	// A compaction's cache is new by design: no avoidable rebuild.
	if got, ok := causeOf(rb, waste.CauseCompaction); ok {
		t.Errorf("compaction counted as avoidable: %+v", got)
	}
	// The ttl cause says which lifetime most of it outlived.
	if ttl, _ := causeOf(rb, waste.CauseTTLExpired); ttl.TTL != "1h" {
		t.Errorf("ttl_expired lifetime = %q, want 1h (two of its three rebuilds)", ttl.TTL)
	}

	// The causes add up to the class, in tokens, dollars, turns and
	// window share; and they come largest first.
	var tokens int64
	var usd, pct float64
	turns := 0
	for i, c := range rb.Causes {
		tokens += c.Tokens
		usd += c.APIEquivalentUSD
		turns += c.Turns
		for _, s := range c.WindowShare {
			pct += s.Pct
		}
		if i > 0 && c.APIEquivalentUSD > rb.Causes[i-1].APIEquivalentUSD {
			t.Errorf("causes out of order: %+v", rb.Causes)
		}
	}
	var classPct float64
	for _, s := range rb.WindowShare {
		classPct += s.Pct
	}
	if tokens != rb.Tokens || math.Abs(usd-rb.APIEquivalentUSD) > 1e-9 || turns != rb.Turns || math.Abs(pct-classPct) > 1e-9 {
		t.Errorf("causes sum to %d tokens $%.4f %d turns %.4f%%; class %d $%.4f %d %.4f%%",
			tokens, usd, turns, pct, rb.Tokens, rb.APIEquivalentUSD, rb.Turns, classPct)
	}
	if rb.Turns != 5 {
		t.Errorf("rebuild turns = %d, want 5", rb.Turns)
	}
	// Other classes carry no causes.
	for _, c := range r.Classes {
		if c.Class != waste.ClassCacheRebuild && len(c.Causes) > 0 {
			t.Errorf("%s has causes", c.Class)
		}
	}
}

// The top cause, as the coach names it.
func TestTopRebuildCause(t *testing.T) {
	r := Build(causesFixture())
	top, ok := class(r, waste.ClassCacheRebuild).TopCause()
	if !ok || top.Cause != waste.CauseTTLExpired {
		t.Fatalf("top cause = %+v, %v", top, ok)
	}
	if _, ok := (ClassCost{}).TopCause(); ok {
		t.Error("a top cause with no rebuilds")
	}
}
