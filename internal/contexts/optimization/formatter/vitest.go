package formatter

import (
	"fmt"
	"strings"
)

// Vitest compresses `vitest` output. Failures are the signal an agent acts
// on and are always kept: the "×" failing-test markers, the "❯ src/….test.ts
// (… | 1 failed)" file lines that carry a failure, the "FAIL" / "⎯⎯⎯ Failed
// Tests" section markers, the "AssertionError"/"Expected"/"Received"
// assertion lines, and the terminal "Test Files … failed" / "Tests …
// failed" summaries. The pure-pass "✓ src/….test.ts (N tests)" file lines
// carry no failure signal and are dropped at Balanced+.
//
// Balanced already removes every passing file line, so Aggressive has
// nothing further to collapse and behaves identically — the byte count is
// monotonic across levels by construction. Non-vitest output that reaches
// this formatter is handed to the generic noise scrub.
type Vitest struct{}

// NewVitest returns the vitest formatter.
func NewVitest() *Vitest { return &Vitest{} }

// Command reports the vitest command token.
func (v *Vitest) Command() string { return "vitest" }

// CriticalLine treats failure signal as critical: lines carrying the "×"
// fail marker or the "❯" file marker (vitest points "❯" at files with
// failures), any "FAIL" or "failed" text (section markers and summaries),
// and the "AssertionError"/"Expected"/"Received" assertion detail. Pure-pass
// "✓" file lines are never critical.
func (v *Vitest) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.Contains(t, "×"),
		strings.Contains(t, "❯"),
		strings.Contains(t, "FAIL"),
		strings.Contains(t, "failed"),
		strings.Contains(t, "AssertionError"),
		strings.Contains(t, "Expected"),
		strings.Contains(t, "Received"):
		return true
	}
	return false
}

// looksLikeVitest reports whether b resembles vitest output.
func looksLikeVitest(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Test Files") ||
		strings.Contains(s, "✓") ||
		strings.Contains(s, "❯") ||
		strings.Contains(s, "vitest") ||
		strings.Contains(s, "RERUN")
}

// isVitestPass reports a pure-pass file line ("✓ src/x.test.ts (5 tests)"),
// dropped at Balanced+.
func isVitestPass(t string) bool {
	return strings.HasPrefix(t, "✓ ")
}

// Format compresses vitest output; non-vitest output falls back to generic.
func (v *Vitest) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeVitest(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "vitest: non-test output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(v, raw, scrubbed, 0, "vitest: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if v.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isVitestPass(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("vitest: %s, %d lines dropped", level, dropped)
	res := enforceCritical(v, raw, compact, dropped, notes)
	return res, true
}
