package formatter

import (
	"fmt"
	"strings"
)

// Jest compresses `jest` output. Failures are the signal an agent acts on
// and are always kept: the per-suite "FAIL path/to/file.test.js" lines, the
// "●" failure-detail blocks (both the "  ● Suite › test" titles and the
// indented diff detail), the "Expected"/"Received" assertion lines, and the
// terminal summary when it reports a failure ("Tests: 2 failed, 40 passed,
// 42 total"). The passing "PASS …" suite lines and interleaved
// "console.log" noise carry no failure signal and are dropped at Balanced+.
//
// Balanced already removes every passing suite line, so Aggressive has
// nothing further to collapse and behaves identically — the byte count is
// monotonic across levels by construction. Non-jest output that reaches
// this formatter is handed to the generic noise scrub.
type Jest struct{}

// NewJest returns the jest formatter.
func NewJest() *Jest { return &Jest{} }

// Command reports the jest command token.
func (j *Jest) Command() string { return "jest" }

// CriticalLine treats failure signal as critical: the "FAIL " suite lines,
// any line carrying the "●" failure marker (titles and detail), the
// "Expected"/"Received" assertion lines, and the terminal summary when it
// reports a failure. Passing "PASS " suite lines are never critical.
func (j *Jest) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "FAIL "),
		strings.Contains(t, "●"),
		strings.Contains(t, "Expected"),
		strings.Contains(t, "Received"),
		isJestFailSummary(t):
		return true
	}
	return false
}

// isJestFailSummary reports a summary line that carries a failure count,
// e.g. "Tests: 2 failed, 40 passed, 42 total".
func isJestFailSummary(t string) bool {
	return strings.Contains(t, "failed")
}

// looksLikeJest reports whether b resembles jest output.
func looksLikeJest(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "PASS ") ||
		strings.Contains(s, "FAIL ") ||
		strings.Contains(s, "Tests:") ||
		strings.Contains(s, "Test Suites:") ||
		strings.Contains(s, "jest")
}

// isJestPass reports a passing suite line ("PASS path/to/file.test.js"),
// dropped at Balanced+.
func isJestPass(t string) bool {
	return strings.HasPrefix(t, "PASS ")
}

// isJestConsole reports interleaved console output noise, dropped at
// Balanced+.
func isJestConsole(t string) bool {
	return strings.Contains(t, "console.log")
}

// Format compresses jest output; non-jest output falls back to generic.
func (j *Jest) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeJest(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "jest: non-test output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(j, raw, scrubbed, 0, "jest: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if j.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isJestPass(t) || isJestConsole(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("jest: %s, %d lines dropped", level, dropped)
	res := enforceCritical(j, raw, compact, dropped, notes)
	return res, true
}
