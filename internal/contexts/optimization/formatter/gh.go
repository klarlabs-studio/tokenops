package formatter

import (
	"fmt"
	"strings"
)

// GH compresses the tabular output of the GitHub CLI's list commands —
// `gh run list`, `gh pr list`, and `gh issue list`. All three emit
// tab-separated rows an agent scans for state to act on:
//
//   - `gh run list` (STATUS TITLE WORKFLOW BRANCH EVENT ID ELAPSED AGE):
//     rows signalling a failed/errored/timed-out run are the signal an
//     agent acts on and are always kept; the long tail of successful runs
//     is noise that collapses to a count at Aggressive.
//   - `gh pr list` / `gh issue list` (number, title, branch, state): open
//     items are state an agent may act on, so every row survives Balanced;
//     failure/error signal remains critical.
//
// A "Showing N of M …" summary preamble carries no state and is stripped at
// Balanced and above. Output that does not resemble gh is handed to the
// generic noise scrub, so the formatter is never destructive on commands it
// does not model.
type GH struct{}

// NewGH returns the gh formatter.
func NewGH() *GH { return &GH{} }

// Command reports the gh command token.
func (g *GH) Command() string { return "gh" }

// ghFailMarkers are the substrings (matched case-insensitively) that mark a
// row as a failure/attention state. Presence of any makes a line critical.
var ghFailMarkers = []string{"fail", "error", "timed_out"}

// CriticalLine treats failure/attention rows as critical: any row whose text
// contains a failure marker ("fail", "error", "timed_out", matched
// case-insensitively) or the cross glyph ("✗" or the ASCII "X" gh prints for
// a failed run). The predicate is kept tight and shape-agnostic so it applies
// equally to `gh run list`, `gh pr list`, and `gh issue list`. A successful /
// passing / open row and the "Showing …" preamble are never critical.
func (g *GH) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	for _, s := range ghFailMarkers {
		if strings.Contains(low, s) {
			return true
		}
	}
	// The cross glyph gh prints for a failed run (unicode ✗ or ASCII X).
	if strings.Contains(t, "✗") || strings.Contains(t, "X") {
		return true
	}
	return false
}

// Format compresses gh list output; non-gh output falls back to generic.
func (g *GH) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeGH(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "gh: non-gh output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(g, raw, scrubbed, 0, "gh: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var success []string // consecutive non-critical rows pending collapse (Aggressive)
	dropped := 0

	// flush emits the pending run of non-critical rows. At Aggressive it
	// collapses them to a count when the summary is genuinely smaller than
	// the listing (the git untracked-block size guard), so Aggressive never
	// grows past Balanced.
	flush := func() {
		if len(success) == 0 {
			return
		}
		if level == LossAggressive {
			listing := strings.Join(success, "\n")
			summary := fmt.Sprintf("  (+%d successful)", len(success))
			if len(summary) < len(listing) {
				kept = append(kept, summary)
				dropped += len(success)
			} else {
				kept = append(kept, success...)
			}
		} else {
			kept = append(kept, success...)
		}
		success = success[:0]
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			dropped++
			continue
		}
		// The "Showing N of M …" summary preamble carries no state.
		if isGHPreamble(t) {
			flush()
			dropped++
			continue
		}
		// A column header (if the caller passed one) is not critical but is
		// kept structurally so column meaning is never collapsed away.
		if isGHHeader(t) {
			flush()
			kept = append(kept, line)
			continue
		}
		if g.CriticalLine(t) {
			flush()
			kept = append(kept, line)
			continue
		}
		// A non-critical (successful / passing / open) row.
		if level == LossAggressive {
			success = append(success, line)
			continue
		}
		kept = append(kept, line)
	}
	flush()

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("gh list: %s, %d lines dropped", level, dropped)
	res := enforceCritical(g, raw, compact, dropped, notes)
	return res, true
}

// looksLikeGH reports whether b resembles gh list output — tab-separated
// rows, the "Showing …" preamble, a run-status token, an open pr/issue
// state, or the literal command token.
func looksLikeGH(b []byte) bool {
	s := string(b)
	if strings.Contains(s, "\t") {
		return true
	}
	return strings.Contains(s, "gh") ||
		strings.Contains(s, "Showing ") ||
		strings.Contains(s, "completed") ||
		strings.Contains(s, "open") ||
		strings.Contains(s, "OPEN")
}

// isGHPreamble reports the "Showing N of M …" summary line gh prints above a
// listing. It carries no state and is dropped at Balanced+.
func isGHPreamble(t string) bool {
	return strings.HasPrefix(t, "Showing ")
}

// isGHHeader reports a gh list column-header line (e.g. "STATUS  TITLE
// WORKFLOW …"). gh's default listing has no header, but a caller that adds
// one gets it preserved rather than collapsed.
func isGHHeader(t string) bool {
	return strings.HasPrefix(t, "STATUS") && strings.Contains(t, "TITLE")
}
