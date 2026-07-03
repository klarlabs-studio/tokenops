package formatter

import (
	"fmt"
	"strings"
)

// Swift compresses the output of `swift build` and `swift test`. A build
// prints per-module/per-file progress ("Compiling Foo module.swift",
// "[1/12] Compiling MyApp main.swift"), compiler diagnostics
// ("/Sources/App/main.swift:10:5: error: cannot find 'foo' in scope"), and a
// terminal "Build complete! (3.42s)" result. `swift test` prints test-suite
// scaffolding ("Test Suite 'All tests' started", "Test Case '-[MyTests
// testExample]' passed (0.01 seconds)") and a "Executed 42 tests, with 1
// failure (0 unexpected) in 0.5 seconds" summary.
//
// Compiler errors (": error:" or a leading "error:"), failing test cases
// (" failed"/"failure"), the "Build complete" result, and the "Executed N
// tests" summary are the signal an agent acts on and are always kept. The
// per-file/per-module compile progress and passing scaffolding are stripped
// at Balanced and above; at Aggressive the advisory ": warning:" diagnostics
// are dropped as well.
//
// Output that does not resemble swift is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Swift struct{}

// NewSwift returns the swift formatter.
func NewSwift() *Swift { return &Swift{} }

// Command reports the swift command token.
func (s *Swift) Command() string { return "swift" }

// CriticalLine treats compiler errors and failure/result signal as critical:
// any ": error:" diagnostic or a leading "error:", a failing test case
// (" failed"/"failure"), the "Build complete" result, and the "Executed N
// tests" summary. Advisory ": warning:" diagnostics, passing test cases, and
// "[N/M] Compiling" progress are never critical.
func (s *Swift) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.Contains(t, ": error:"),
		strings.HasPrefix(t, "error:"),
		strings.Contains(t, " failed"),
		strings.Contains(t, "failure"),
		strings.HasPrefix(t, "Build complete"),
		isSwiftExecutedSummary(t):
		return true
	}
	return false
}

// isSwiftExecutedSummary reports the "Executed N tests, …" test summary line.
func isSwiftExecutedSummary(t string) bool {
	return strings.HasPrefix(t, "Executed ") && strings.Contains(t, " tests")
}

// looksLikeSwift reports whether b resembles swift build/test output.
func looksLikeSwift(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "swift") ||
		strings.Contains(s, "Compiling ") ||
		strings.Contains(s, "Build complete") ||
		strings.Contains(s, "Test Suite") ||
		strings.Contains(s, "Test Case") ||
		strings.Contains(s, ".swift:")
}

// isSwiftCompileProgress reports the transient per-file/per-module compile
// progress swift emits while building ("[1/12] Compiling MyApp main.swift",
// "Compiling Foo module.swift"). It carries no diagnostic signal and is safe
// to drop at Balanced+.
func isSwiftCompileProgress(t string) bool {
	if strings.HasPrefix(t, "[") {
		if idx := strings.Index(t, "] Compiling"); idx > 0 {
			return true
		}
	}
	return strings.HasPrefix(t, "Compiling ")
}

// isSwiftPassingScaffold reports the passing test-suite scaffolding that
// carries no failure signal and is safe to drop at Balanced+: passing test
// cases and test-suite start/pass banners.
func isSwiftPassingScaffold(t string) bool {
	switch {
	case strings.HasPrefix(t, "Test Case ") && strings.Contains(t, " passed"),
		strings.HasPrefix(t, "Test Suite ") && strings.Contains(t, " started"),
		strings.HasPrefix(t, "Test Suite ") && strings.Contains(t, " passed"):
		return true
	}
	return false
}

// isSwiftWarning reports the advisory warning diagnostics swiftc prints. They
// are informational, not blocking, so Aggressive drops them.
func isSwiftWarning(t string) bool {
	return strings.Contains(t, ": warning:")
}

// Format compresses swift output; non-swift output falls back to generic.
func (s *Swift) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeSwift(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "swift: non-swift output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(s, raw, scrubbed, 0, "swift: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if s.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isSwiftCompileProgress(t) || isSwiftPassingScaffold(t) {
			dropped++
			continue
		}
		if level == LossAggressive && isSwiftWarning(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("swift: %s, %d lines dropped", level, dropped)
	res := enforceCritical(s, raw, compact, dropped, notes)
	return res, true
}
