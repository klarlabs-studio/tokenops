package replies

import (
	"fmt"
	"sort"
)

// Recommendation turns reply statistics into something an operator can
// act on, in the units they budget in.
//
// The reply coach measured article and filler density and stopped there —
// a table of ratios next to a prompt coach that converts patterns into
// turns, tokens, dollars and hours. Densities describe; they do not tell
// anyone what to change.
type Recommendation struct {
	// Title is the change being proposed.
	Title string
	// Evidence is the measurement the proposal rests on, so the operator
	// can judge it rather than take it on faith.
	Evidence string
	// Action is the concrete thing to do.
	Action string
	// TargetAvgWords is the words-per-reply the operator already achieves
	// in their leanest substantial sessions.
	TargetAvgWords float64
	// CurrentAvgWords is their overall average.
	CurrentAvgWords float64
	// EstimatedTokensSaved projects the output tokens the whole corpus
	// would not have spent at the target rate.
	EstimatedTokensSaved int64
}

const (
	// minRepliesForTarget keeps a two-reply session from setting the goal
	// for every other session. Short sessions are noise.
	minRepliesForTarget = 25
	// minSpreadRatio is how much leaner the target must be before the
	// advice is worth giving. Below it the operator is already
	// consistent, and manufacturing a recommendation from noise is how
	// coaching loses trust.
	minSpreadRatio = 0.75
	// wordsToTokens converts English words to tokens. Deliberately
	// conservative so the projection understates rather than oversells.
	wordsToTokens = 1.3
)

// Recommend derives an output-brevity recommendation from the operator's
// own sessions, or reports that none is warranted.
//
// The target is the leanest substantial session they have actually run,
// never an invented ideal. An external "typical English" figure would be
// a target nobody has demonstrated is achievable for this work; their own
// lean session is proof by construction that the same work fits in fewer
// words.
func Recommend(f Findings) (Recommendation, bool) {
	if f.TotalReplies == 0 || f.Baseline.AvgWords <= 0 {
		return Recommendation{}, false
	}

	candidates := make([]SessionStat, 0, len(f.BySession))
	for _, s := range f.BySession {
		if s.Stats.Replies >= minRepliesForTarget && s.Stats.AvgWords > 0 {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) < 2 {
		return Recommendation{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Stats.AvgWords < candidates[j].Stats.AvgWords
	})
	target := candidates[0]

	if target.Stats.AvgWords > f.Baseline.AvgWords*minSpreadRatio {
		return Recommendation{}, false
	}

	wordsSaved := (f.Baseline.AvgWords - target.Stats.AvgWords) * float64(f.TotalReplies)
	tokens := int64(wordsSaved * wordsToTokens)
	if tokens <= 0 {
		return Recommendation{}, false
	}

	return Recommendation{
		Title: "Ask for the output density you already get in your leanest sessions",
		Evidence: fmt.Sprintf(
			"session %s averaged %.0f words/reply across %d replies, against %.0f overall — the same work, %.0f%% fewer words",
			shortID(target.SessionID), target.Stats.AvgWords, target.Stats.Replies,
			f.Baseline.AvgWords,
			(1-target.Stats.AvgWords/f.Baseline.AvgWords)*100),
		Action:               "set an output-brevity instruction in CLAUDE.md (or a compact output style) so every session starts where your leanest one landed",
		TargetAvgWords:       target.Stats.AvgWords,
		CurrentAvgWords:      f.Baseline.AvgWords,
		EstimatedTokensSaved: tokens,
	}, true
}

// shortID trims a session UUID to its first segment for display.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
