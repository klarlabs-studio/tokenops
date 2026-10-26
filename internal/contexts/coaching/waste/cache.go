package waste

import "time"

// Prompt-cache lifetimes. Anthropic keeps a cached prefix for five minutes
// after it was last used, or an hour when it was written with the extended
// lifetime; each read or write starts the lifetime again.
const (
	CacheTTL         = 5 * time.Minute
	CacheTTLExtended = time.Hour
)

// CacheExpiringWithin is how close to expiry a session's cache counts as
// about to go cold: long enough to send something before it does.
const CacheExpiringWithin = 2 * time.Minute

// RebuildCause is why a turn wrote its prompt cache again.
//
// A change to the tool or MCP set also invalidates the prefix, but no local
// record shows it apart from other prefix changes: Claude Code's transcript
// logs tool deltas that it appends after the prefix, not changes to the
// tool list the prefix starts with. Such a rebuild reads as prefix_changed;
// no cause is guessed for it.
type RebuildCause string

// The causes, in the order they are tested: the most definite first.
const (
	// CauseCompaction is a rebuild after a compaction or summary: the
	// context fell to under half of the turn before's, so its cache is new
	// by design.
	CauseCompaction RebuildCause = "compaction"
	// CauseModelSwitch is a turn on another model than the turn before: a
	// cache belongs to one model.
	CauseModelSwitch RebuildCause = "model_switch"
	// CauseTTLExpired is a turn sent longer after the one before than the
	// cache's lifetime.
	CauseTTLExpired RebuildCause = "ttl_expired"
	// CausePrefixChanged is any other rebuild after the turn before read
	// from the cache: something early in the prompt changed (a timestamp,
	// a reordered or changed tool set, edited instructions).
	CausePrefixChanged RebuildCause = "prefix_changed"
	// CauseUnknown is a rebuild the evidence cannot explain: the turn
	// before had no cache hits, or no times were recorded.
	CauseUnknown RebuildCause = "unknown"
)

// RebuildCauses lists the causes in the order they are tested.
func RebuildCauses() []RebuildCause {
	return []RebuildCause{CauseCompaction, CauseModelSwitch, CauseTTLExpired, CausePrefixChanged, CauseUnknown}
}

var causeNames = map[RebuildCause]string{
	CauseCompaction:    "compactions",
	CauseModelSwitch:   "model switches",
	CauseTTLExpired:    "idle gaps past the cache lifetime",
	CausePrefixChanged: "prompt prefix changes",
	CauseUnknown:       "unexplained",
}

// RebuildCauseName is how a cause reads in a sentence.
func RebuildCauseName(c RebuildCause) string {
	if n, ok := causeNames[c]; ok {
		return n
	}
	return string(c)
}

// Avoidable reports whether a rebuild with this cause could have been
// avoided: all but a compaction's, whose cache is new by design.
func (c RebuildCause) Avoidable() bool { return c != CauseCompaction }

// CacheTurn is one turn's prompt-cache figures: token counts, times and the
// model, never the prompt.
type CacheTurn struct {
	// At is when the turn was recorded; Latency how long it took, zero when
	// unknown, so the request went out at At minus Latency.
	At      time.Time
	Latency time.Duration
	Model   string
	// Input includes the cache portions: CacheRead read from the cache,
	// CacheWrite written to it (CacheWrite1h of them with the hour-long
	// lifetime).
	Input, CacheRead, CacheWrite, CacheWrite1h int64
}

// Rebuild is a turn that wrote again most of the prefix the turn before had
// cached.
type Rebuild struct {
	Cause RebuildCause
	// Tokens is the part of the turn's write the turn before had cached.
	Tokens int64
}

// ClassifyRebuild reports whether cur rebuilt the cache prev left, and why.
// ttl is the lifetime prev's cache had. A rebuild writes more than half of
// what prev cached; a turn whose context fell to under half of prev's is a
// compaction, whatever it wrote.
func ClassifyRebuild(prev, cur CacheTurn, ttl time.Duration) (Rebuild, bool) {
	cached := prev.CacheRead + prev.CacheWrite
	if cached <= 0 || cur.CacheWrite <= 0 {
		return Rebuild{}, false
	}
	if cur.Input*2 < prev.Input {
		return Rebuild{Cause: CauseCompaction, Tokens: cur.CacheWrite}, true
	}
	rebuilt := min(cur.CacheWrite, cached-cur.CacheRead)
	if rebuilt*2 <= cached {
		return Rebuild{}, false
	}
	r := Rebuild{Tokens: rebuilt}
	timed := !prev.At.IsZero() && !cur.At.IsZero()
	switch {
	case prev.Model != "" && cur.Model != "" && prev.Model != cur.Model:
		r.Cause = CauseModelSwitch
	case timed && ttl > 0 && cur.At.Sub(prev.At) > ttl:
		r.Cause = CauseTTLExpired
	case timed && prev.CacheRead > 0:
		r.Cause = CausePrefixChanged
	default:
		r.Cause = CauseUnknown
	}
	return r, true
}

// TTLOf is the lifetime a turn's cache write bought; false when it wrote
// nothing.
func TTLOf(t CacheTurn) (time.Duration, bool) {
	switch {
	case t.CacheWrite <= 0:
		return 0, false
	case t.CacheWrite1h > 0:
		return CacheTTLExtended, true
	}
	return CacheTTL, true
}

// ColdClock is what a session's cache countdown runs from: when its last
// request went out, and the lifetime its latest write bought (the shorter
// one when no write is on record). False when no turn used the cache.
func ColdClock(turns []CacheTurn) (time.Time, time.Duration, bool) {
	var last time.Time
	ttl, used := CacheTTL, false
	for _, t := range turns {
		if t.CacheRead <= 0 && t.CacheWrite <= 0 {
			continue
		}
		used = true
		if sent := t.At.Add(-t.Latency); sent.After(last) {
			last = sent
		}
		if d, wrote := TTLOf(t); wrote {
			ttl = d
		}
	}
	return last, ttl, used && !last.IsZero()
}

// CacheState is where a session's cache stands.
type CacheState string

// The states.
const (
	CacheWarm     CacheState = "warm"
	CacheExpiring CacheState = "expiring"
	CacheCold     CacheState = "cold"
)

// CacheCountdown is the time until a session's prompt cache goes cold.
type CacheCountdown struct {
	// Client is the agent whose session it is.
	Client        string    `json:"client"`
	LastRequestAt time.Time `json:"last_request_at"`
	TTLSeconds    int64     `json:"ttl_seconds"`
	ExpiresAt     time.Time `json:"expires_at"`
	// RemainingSeconds is zero once the cache is cold.
	RemainingSeconds int64      `json:"remaining_seconds"`
	State            CacheState `json:"state"`
}

// Countdown is the cache's countdown at now: warm, expiring with under
// CacheExpiringWithin left, or cold from the moment it expires.
func Countdown(lastRequest time.Time, ttl time.Duration, now time.Time) CacheCountdown {
	expires := lastRequest.Add(ttl)
	left := expires.Sub(now)
	c := CacheCountdown{LastRequestAt: lastRequest, TTLSeconds: int64(ttl / time.Second), ExpiresAt: expires, State: CacheWarm}
	switch {
	case left <= 0:
		c.State = CacheCold
	case left < CacheExpiringWithin:
		c.State = CacheExpiring
	}
	if left > 0 {
		c.RemainingSeconds = int64(left / time.Second)
	}
	return c
}
