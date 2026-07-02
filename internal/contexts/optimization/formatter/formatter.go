// Package formatter hosts the deterministic command-output compression
// engine. Given a command name and the raw bytes a shell command emitted,
// a Formatter produces a compact rendering that preserves every line the
// downstream agent must not lose — errors, failures, changed state — while
// dropping noise (progress bars, banners, unchanged-file listings).
//
// The engine is intentionally free of heuristics or model calls: each
// Formatter is a pure function whose behaviour is fixed by rules and proven
// by the golden eval corpus in this context. Two guarantees hold for every
// Formatter regardless of loss level:
//
//   - Determinism: Format(raw, level) is a pure function of (raw, level).
//     No clocks, no randomness, no I/O. The same input always yields the
//     same output, so the golden corpus is a stable contract.
//   - Critical-line survival: every line a Formatter classifies as
//     critical (via its CriticalLine predicate) appears verbatim in the
//     compact output. A Formatter that would drop a critical line must
//     instead fall back to the raw output and report CriticalKept=false so
//     the caller can recover.
//
// Loss level is configured per command by the caller (see LossLevel). A
// formatter reads the level to decide how much noise to strip, but the
// critical-line guarantee is invariant across all levels.
package formatter

import "strings"

// LossLevel selects how aggressively a Formatter strips non-critical
// content. Critical lines survive at every level; the level only governs
// how much of the remaining noise is removed.
type LossLevel int

const (
	// LossConservative removes only unambiguous noise: trailing
	// whitespace, blank-line runs, exact duplicate lines, ANSI escape
	// sequences. Nothing semantic is dropped. Safe default for unknown
	// or high-stakes commands.
	LossConservative LossLevel = iota
	// LossBalanced additionally drops command-specific noise a formatter
	// declares safe (progress lines, banners, "up to date" chatter) while
	// keeping all state-bearing and error lines.
	LossBalanced
	// LossAggressive collapses repetitive state-bearing groups into
	// summaries (e.g. "+142 unchanged files") on top of Balanced. Highest
	// savings; relies on the recovery store for full detail on demand.
	LossAggressive
)

// String renders the level as its config token.
func (l LossLevel) String() string {
	switch l {
	case LossConservative:
		return "conservative"
	case LossBalanced:
		return "balanced"
	case LossAggressive:
		return "aggressive"
	default:
		return "conservative"
	}
}

// ParseLossLevel maps a config token to a LossLevel. Unknown tokens fall
// back to LossConservative and report ok=false so callers can warn.
func ParseLossLevel(s string) (LossLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "conservative", "":
		return LossConservative, true
	case "balanced":
		return LossBalanced, true
	case "aggressive":
		return LossAggressive, true
	default:
		return LossConservative, false
	}
}

// Result is the outcome of formatting one command's output. Compact is the
// text the caller forwards to the agent; the byte counters and CriticalKept
// flag let the caller emit an accurate optimization event and decide
// whether recovery is warranted.
type Result struct {
	// Compact is the rewritten output. When a formatter bails out (see
	// CriticalKept) Compact equals the raw input.
	Compact []byte
	// BytesBefore / BytesAfter are the raw and compact byte lengths.
	BytesBefore int
	BytesAfter  int
	// LinesDropped counts source lines removed or collapsed.
	LinesDropped int
	// CriticalKept reports whether every critical line survived. When
	// false the formatter refused to compress (Compact == raw) because it
	// could not guarantee survival; the caller should forward raw output.
	CriticalKept bool
	// Notes is a short, deterministic human-readable summary of what the
	// formatter did, suitable for the optimization event Reason field.
	Notes string
}

// SavedBytes returns the byte reduction, never negative.
func (r Result) SavedBytes() int {
	if r.BytesAfter > r.BytesBefore {
		return 0
	}
	return r.BytesBefore - r.BytesAfter
}

// Formatter compresses the output of one command family deterministically.
// Implementations MUST be pure and safe for concurrent use.
type Formatter interface {
	// Command reports the command token this formatter handles (e.g.
	// "git", "npm"). The registry keys on the first output-producing
	// argv token; subcommand dispatch is the formatter's concern.
	Command() string
	// CriticalLine reports whether a single output line must survive
	// compression verbatim. The engine and the eval corpus both call this
	// to enforce the survival guarantee, so it must itself be pure.
	CriticalLine(line string) bool
	// Format compresses raw at the given loss level. It returns ok=false
	// only for inputs it declines to handle at all (e.g. empty input);
	// a formatter that cannot safely compress must still return ok=true
	// with a Result whose CriticalKept is false and Compact is raw.
	Format(raw []byte, level LossLevel) (Result, bool)
}

// enforceCritical is the shared guard every Formatter runs before
// returning a compacted Result: it re-checks that no critical line present
// in the raw input was dropped from the compact output. On violation it
// returns the raw-passthrough Result so the invariant can never be broken
// by a formatter bug — the corpus proves formatters rarely hit this path,
// and production never loses a critical line even if one does.
func enforceCritical(f Formatter, raw, compact []byte, linesDropped int, notes string) Result {
	rawCritical := criticalSet(f, raw)
	if len(rawCritical) > 0 {
		compactCritical := criticalSet(f, compact)
		for line := range rawCritical {
			if _, ok := compactCritical[line]; !ok {
				return rawPassthrough(raw, "critical_line_would_drop")
			}
		}
	}
	return Result{
		Compact:      compact,
		BytesBefore:  len(raw),
		BytesAfter:   len(compact),
		LinesDropped: linesDropped,
		CriticalKept: true,
		Notes:        notes,
	}
}

// rawPassthrough builds the bail-out Result: no compression, invariant
// preserved by construction.
func rawPassthrough(raw []byte, reason string) Result {
	return Result{
		Compact:      raw,
		BytesBefore:  len(raw),
		BytesAfter:   len(raw),
		LinesDropped: 0,
		CriticalKept: false,
		Notes:        reason,
	}
}

// criticalSet returns the trimmed critical lines present in b.
func criticalSet(f Formatter, b []byte) map[string]struct{} {
	set := make(map[string]struct{})
	for line := range strings.SplitSeq(string(b), "\n") {
		t := strings.TrimRight(line, " \t\r")
		if f.CriticalLine(t) {
			set[strings.TrimSpace(t)] = struct{}{}
		}
	}
	return set
}
