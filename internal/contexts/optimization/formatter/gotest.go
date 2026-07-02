package formatter

import (
	"fmt"
	"strings"
)

// GoTest compresses `go test` output. Failures, panics, and build errors
// are the signal an agent acts on and are always kept; the per-test
// scaffolding (=== RUN, --- PASS, === CONT) is noise dropped at Balanced+.
// At Aggressive the passing per-package summary lines ("ok  pkg …")
// collapse to a single count so a large green run shrinks to its failures.
//
// Non-test go output (build, vet, run) is handed to the generic scrub.
type GoTest struct{}

// NewGoTest returns the go test formatter.
func NewGoTest() *GoTest { return &GoTest{} }

// Command reports the go command token.
func (g *GoTest) Command() string { return "go" }

// CriticalLine treats failure and error signal as critical: failing test
// markers, panics, the terminal FAIL line, and compiler/vet errors
// (path:line:col: message). Passing scaffolding is never critical.
func (g *GoTest) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "--- FAIL"),
		strings.HasPrefix(t, "FAIL"),
		strings.HasPrefix(t, "panic:"),
		strings.HasPrefix(t, "--- SKIP"),
		strings.Contains(t, "[build failed]"),
		strings.Contains(t, "[setup failed]"),
		isGoCompileError(t):
		return true
	}
	return false
}

// isGoCompileError matches "path.go:line:col: message" and "path.go:line:
// message" — the shape go build / vet emit for source errors.
func isGoCompileError(t string) bool {
	_, rest, found := strings.Cut(t, ".go:")
	if !found {
		return false
	}
	// Require at least a line number right after ".go:".
	return len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9'
}

// looksLikeGoTest reports whether b resembles `go test` output.
func looksLikeGoTest(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "=== RUN") ||
		strings.Contains(s, "--- FAIL") ||
		strings.Contains(s, "--- PASS") ||
		strings.Contains(s, "\nok  \t") ||
		strings.HasPrefix(s, "ok  \t") ||
		strings.Contains(s, "\nFAIL\t") ||
		strings.Contains(s, "no test files")
}

// goScaffold reports the per-test scaffolding lines that carry no failure
// signal and are safe to drop at Balanced+.
func goScaffold(t string) bool {
	switch {
	case strings.HasPrefix(t, "=== RUN"),
		strings.HasPrefix(t, "=== CONT"),
		strings.HasPrefix(t, "=== PAUSE"),
		strings.HasPrefix(t, "=== NAME"),
		strings.HasPrefix(t, "--- PASS"),
		t == "PASS":
		return true
	}
	return false
}

// isGoPassPkg reports the per-package success summary ("ok  \tpkg\t0.1s"),
// collapsed at Aggressive.
func isGoPassPkg(t string) bool {
	return strings.HasPrefix(t, "ok  \t") || strings.HasPrefix(t, "ok \t")
}

// Format compresses go test output; non-test output falls back to generic.
func (g *GoTest) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeGoTest(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "go: non-test output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(g, raw, scrubbed, 0, "go test: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	passPkgs := 0
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if g.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if goScaffold(t) {
			dropped++
			continue
		}
		if level == LossAggressive && isGoPassPkg(t) {
			passPkgs++
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	if passPkgs > 0 {
		kept = append(kept, fmt.Sprintf("ok\t(+%d passing packages)", passPkgs))
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("go test: %s, %d lines dropped", level, dropped)
	res := enforceCritical(g, raw, compact, dropped, notes)
	return res, true
}
