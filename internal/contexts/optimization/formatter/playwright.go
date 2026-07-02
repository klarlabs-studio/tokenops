package formatter

import (
	"fmt"
	"strings"
)

// Playwright compresses `playwright test` output. The failed-test lines
// (marked ✘/✗), the "Error:" and "expect(...)" assertion detail, timeout
// notices, and the terminal summary when it reports a failure are the
// signal an agent acts on and are always kept. The "Running N tests using M
// workers" preamble and the passing per-test lines (marked ✓) are noise
// dropped at Balanced+.
//
// Non-playwright output that reaches this formatter is handed to the generic
// noise scrub.
type Playwright struct{}

// NewPlaywright returns the playwright formatter.
func NewPlaywright() *Playwright { return &Playwright{} }

// Command reports the playwright command token.
func (p *Playwright) Command() string { return "playwright" }

// CriticalLine treats failure signal as critical: failed-test lines (✘/✗),
// "Error:" lines, "expect(" assertion lines, timeout notices, and the
// terminal summary when it reports a failure. Passing (✓) lines are never
// critical.
func (p *Playwright) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.Contains(t, "✘"),
		strings.Contains(t, "✗"),
		strings.HasPrefix(t, "Error:"),
		strings.Contains(t, "expect("),
		strings.Contains(t, "Timed out"),
		strings.Contains(t, "failed"):
		return true
	}
	return false
}

// looksLikePlaywright reports whether b resembles playwright test output.
func looksLikePlaywright(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Running ") ||
		strings.Contains(s, "workers") ||
		strings.Contains(s, "✓") ||
		strings.Contains(s, "✘") ||
		strings.Contains(s, "playwright") ||
		strings.Contains(s, "passed (")
}

// isPlaywrightPass reports a passing per-test line (marked ✓), dropped at
// Balanced+.
func isPlaywrightPass(t string) bool {
	return strings.Contains(t, "✓")
}

// isPlaywrightPreamble reports the "Running N tests using M workers" banner,
// dropped at Balanced+.
func isPlaywrightPreamble(t string) bool {
	return strings.HasPrefix(t, "Running ") && strings.Contains(t, "using")
}

// Format compresses playwright output; non-playwright output falls back to
// generic.
func (p *Playwright) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikePlaywright(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "playwright: non-playwright output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(p, raw, scrubbed, 0, "playwright: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if p.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isPlaywrightPass(t) || isPlaywrightPreamble(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("playwright: %s, %d lines dropped", level, dropped)
	res := enforceCritical(p, raw, compact, dropped, notes)
	return res, true
}
