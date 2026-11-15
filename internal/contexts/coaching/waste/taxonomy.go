package waste

import "sort"

// Class is one kind of avoidable usage in the waste taxonomy (#655). The
// classes overlap in what they look at — an abandoned session can also
// have run oversized — so a token is attributed to at most one of them,
// the first in Classes order that claims it.
type Class string

// The taxonomy, in attribution order: the most specific cause first, the
// catch-all last.
const (
	// ClassReread is a file read again, in full and unchanged, while its
	// earlier read was still in context: what the read guard would have
	// refused, read anyway.
	ClassReread Class = "redundant_reread"
	// ClassCacheRebuild is a prompt cache written again for a prefix that
	// was cached a turn earlier: the cache expired or was invalidated, so
	// context billed as a cache read was billed as a write instead.
	ClassCacheRebuild Class = "cache_rebuild"
	// ClassIdleRebuild is a cache rebuilt after an idle break: the request
	// went out longer after the one before than the cache's lifetime, so
	// the prefix the session had cached went cold while the developer was
	// away. It is the cache-rebuild cause a setting or habit can prevent.
	ClassIdleRebuild Class = "idle_cache_rebuild"
	// ClassOverTiered is work run on a dearer model than its kind needs,
	// after the coach advised a cheaper one.
	ClassOverTiered Class = "over_tiered_model"
	// ClassRetryLoop is retry and edit loops and runaway context growth:
	// an identical prompt sent again, one agent firing over and over,
	// context appended faster than work needs.
	ClassRetryLoop Class = "retry_loop"
	// ClassOversized is context carried above the line a session should
	// have compacted at, or above the workflow's context ceiling.
	ClassOversized Class = "oversized_context"
	// ClassStopped is work the developer stopped: the turn in flight when
	// the developer interrupted an instruction, which the stop discarded.
	ClassStopped Class = "stopped_turns"
	// ClassAbandoned is a session that ended with no commit and no
	// recorded outcome: work with nothing to show for it.
	ClassAbandoned Class = "abandoned_session"
)

// Classes lists the taxonomy in attribution order.
func Classes() []Class {
	return []Class{ClassReread, ClassCacheRebuild, ClassIdleRebuild, ClassOverTiered, ClassRetryLoop, ClassOversized, ClassStopped, ClassAbandoned}
}

// RebuildClass is the class a cache rebuild with this cause is counted
// under: an idle gap past the cache lifetime has its own class, the rest
// are cache rebuilds. A compaction is not avoidable and has none.
func RebuildClass(c RebuildCause) Class {
	if c == CauseTTLExpired {
		return ClassIdleRebuild
	}
	return ClassCacheRebuild
}

var classNames = map[Class]string{
	ClassReread:       "redundant re-reads",
	ClassCacheRebuild: "cache-miss rebuilds",
	ClassIdleRebuild:  "idle cache rebuilds",
	ClassOverTiered:   "over-tiered model",
	ClassRetryLoop:    "retry loops and runaway growth",
	ClassOversized:    "oversized context",
	ClassStopped:      "stopped turns",
	ClassAbandoned:    "abandoned sessions",
}

// fixes are the setting or habit that prevents each class, in the words
// a developer acts on.
var fixes = map[Class]string{
	ClassReread:       "let the read guard refuse unchanged re-reads (tokenops coach set read-guard)",
	ClassCacheRebuild: "keep the prompt prefix stable: no timestamps or reordered tools near the top of the prompt",
	ClassIdleRebuild:  "keep a long session warm before the cache lapses, or use the one-hour cache lifetime when the client offers it",
	ClassOverTiered:   "run the work the coach named on the cheaper model it advised",
	ClassRetryLoop:    "stop a repeated prompt or agent loop after a few tries, and break the loop rather than retry it",
	ClassOversized:    "compact the context before it passes the line (auto-compact at the derived line, or /compact by hand)",
	ClassStopped:      "state the scope and the done condition in the first instruction, so the agent does not start work to be stopped",
	ClassAbandoned:    "end a session that has no task left, or open a fresh one for the next task",
}

// FixOf is the setting or habit that prevents a class; empty for an
// unknown class.
func FixOf(c Class) string { return fixes[c] }

// ClassName is how a class reads in a sentence.
func ClassName(c Class) string {
	if n, ok := classNames[c]; ok {
		return n
	}
	return string(c)
}

// Claim is one source's case that some tokens were avoidable: Tokens of
// the turns at the indexes in Turns.
type Claim struct {
	Class  Class
	Turns  []int
	Tokens int64
}

// Attribute gives each claim its tokens out of the turns' budgets (each
// turn's tokens), so that no turn gives away more than it has and no
// token goes to two classes. Claims are settled in Classes order, and in
// the order given within a class; a claim larger than what its turns have
// left takes what is left. A claim spread over several turns draws from
// each in proportion to what it has left. The result holds, per turn,
// the tokens each class was given.
func Attribute(budget []int64, claims []Claim) []map[Class]int64 {
	left := append([]int64(nil), budget...)
	out := make([]map[Class]int64, len(budget))
	for i := range out {
		out[i] = map[Class]int64{}
	}
	rank := map[Class]int{}
	for i, c := range Classes() {
		rank[c] = i
	}
	ordered := append([]Claim(nil), claims...)
	sort.SliceStable(ordered, func(i, j int) bool { return rank[ordered[i].Class] < rank[ordered[j].Class] })
	for _, c := range ordered {
		for i, n := range draw(left, c.Turns, c.Tokens) {
			left[i] -= n
			out[i][c.Class] += n
		}
	}
	return out
}

// draw takes want tokens from the turns at idx in proportion to what each
// has left, never more than that.
func draw(left []int64, idx []int, want int64) map[int]int64 {
	if want <= 0 {
		return nil
	}
	var turns []int
	var avail int64
	seen := map[int]bool{}
	for _, i := range idx {
		if i < 0 || i >= len(left) || seen[i] || left[i] <= 0 {
			continue
		}
		seen[i] = true
		turns = append(turns, i)
		avail += left[i]
	}
	if avail == 0 {
		return nil
	}
	got := make(map[int]int64, len(turns))
	if want >= avail {
		for _, i := range turns {
			got[i] = left[i]
		}
		return got
	}
	var given int64
	for _, i := range turns {
		// want < avail, so each share is below left[i] and the product
		// stays well inside int64 for token counts.
		n := int64(float64(want) * float64(left[i]) / float64(avail))
		got[i] = n
		given += n
	}
	// Rounding leaves a few tokens; the first turns with room take them.
	for _, i := range turns {
		if given >= want {
			break
		}
		room := min(left[i]-got[i], want-given)
		got[i] += room
		given += room
	}
	return got
}
