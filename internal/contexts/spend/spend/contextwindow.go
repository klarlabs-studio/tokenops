package spend

import "strings"

// Context window sizes in tokens.
const (
	// WindowStandard is the long-standing Claude context window.
	WindowStandard int64 = 200_000
	// WindowLong is the 1M window Claude 4.6 and later carry at standard
	// pricing.
	WindowLong int64 = 1_000_000
)

// longWindowPrefixes are the model families documented as carrying the
// full 1M context window at standard pricing (Anthropic pricing page,
// retrieved 2026-08-23: "Claude 4.6 and later models and Claude Mythos
// Preview include the full 1M token context window").
//
// Confirmed against real transcripts: the largest single turn observed on
// claude-opus-5 was 999,947 tokens.
var longWindowPrefixes = []string{
	"claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6",
	"claude-sonnet-5", "claude-fable-5", "claude-mythos",
}

// standardWindowPrefixes are the families that stayed at 200k.
var standardWindowPrefixes = []string{
	"claude-opus-4-5", "claude-opus-4-1", "claude-opus-4",
	"claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet-4",
	"claude-haiku-4-5", "claude-haiku-3-5", "claude-3-5",
	"claude-3-7", "claude-3-opus", "claude-3-haiku",
}

// ContextWindow returns the model's context window in tokens, and whether
// one is known.
//
// An unrecognised model reports no window rather than a plausible
// default. A percentage computed against a guessed denominator is worse
// than no percentage at all: it looks authoritative and is not, which is
// the failure mode this codebase has spent considerable effort undoing.
func ContextWindow(model string) (int64, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return 0, false
	}
	// An explicit [1m] suffix is the operator naming the variant they
	// enabled; it outranks whatever the family default would be.
	if strings.Contains(m, "[1m]") {
		return WindowLong, true
	}
	// Longest match first: claude-sonnet-4-6 must not be caught by a
	// shorter claude-sonnet-4 prefix with a different window.
	if w, ok := matchLongest(m, standardWindowPrefixes, longWindowPrefixes); ok {
		return w, true
	}
	return 0, false
}

// matchLongest picks whichever prefix in either set matches m and is
// longest, so a more specific family always wins over a shorter one that
// happens to be its prefix.
func matchLongest(m string, standard, long []string) (int64, bool) {
	var (
		bestLen int
		bestVal int64
		found   bool
	)
	consider := func(prefixes []string, window int64) {
		for _, p := range prefixes {
			if strings.HasPrefix(m, p) && len(p) > bestLen {
				bestLen, bestVal, found = len(p), window, true
			}
		}
	}
	consider(standard, WindowStandard)
	consider(long, WindowLong)
	return bestVal, found
}
