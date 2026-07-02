package formatter

import (
	"fmt"
	"strings"
)

// Mix compresses the output of `mix compile` / `mix test` (Elixir) runs. Mix
// emits progress markers — "==> myapp" project banners, "Compiling 24 files
// (.ex)", "Generated myapp app" — plus "warning: …" advisories with their
// following location lines. The failure and result signal an agent acts on —
// Elixir errors ("** (CompileError) …", "** (RuntimeError) …"), test-failure
// headers ("  1) test something (MyTest)"), "error:" lines, and the
// "N tests, N failures" summary — is always kept; the compile progress
// markers are noise stripped at Balanced and above. At Aggressive the
// "warning:" advisories and their location lines are dropped as well.
//
// Output that does not resemble mix is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type Mix struct{}

// NewMix returns the mix formatter.
func NewMix() *Mix { return &Mix{} }

// Command reports the mix command token.
func (m *Mix) Command() string { return "mix" }

// CriticalLine treats failure and result signal as critical: Elixir error
// tuples ("** (…"), numbered test-failure headers ("  N) test …"), "error:"
// lines, and any summary line reporting a failure ("… failure"). The
// "Compiling"/"Generated" progress markers and "warning:" advisories are
// never critical.
func (m *Mix) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.Contains(t, "** ("),
		strings.HasPrefix(t, "error:"),
		strings.Contains(t, "failure"):
		return true
	case isMixTestFailure(t):
		return true
	}
	return false
}

// isMixTestFailure reports a numbered test-failure header ("N) test …"),
// which ExUnit prints (indented) for each failing test.
func isMixTestFailure(t string) bool {
	// A failure header starts with a digit run, then ") test".
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	return strings.HasPrefix(t[i:], ") test")
}

// looksLikeMix reports whether b resembles mix compile/test output.
func looksLikeMix(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "==> ") ||
		strings.Contains(s, "Compiling ") ||
		strings.Contains(s, "Generated ") ||
		strings.Contains(s, "mix") ||
		strings.Contains(s, "tests,") ||
		strings.Contains(s, " test ")
}

// isMixProgress reports the compile progress markers ("==> myapp",
// "Compiling 24 files (.ex)", "Generated myapp app") that carry no state and
// are safe to drop at Balanced+.
func isMixProgress(t string) bool {
	switch {
	case strings.HasPrefix(t, "==> "),
		strings.HasPrefix(t, "Compiling "),
		strings.HasPrefix(t, "Generated "):
		return true
	}
	return false
}

// isMixWarning reports the "warning: …" advisory lines and the indented
// source-location line mix prints beneath each, dropped at Aggressive.
func isMixWarning(t string) bool {
	if strings.HasPrefix(t, "warning:") {
		return true
	}
	// The location line follows the warning: "  lib/foo.ex:10" — an indented
	// path with a ".ex:"/".exs:" line reference and no failure signal.
	return (strings.Contains(t, ".ex:") || strings.Contains(t, ".exs:")) &&
		!strings.Contains(t, "** (")
}

// Format compresses mix output; non-mix output falls back to generic.
func (m *Mix) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeMix(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "mix: non-mix output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(m, raw, scrubbed, 0, "mix: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if m.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isMixProgress(t) || t == "" {
			dropped++
			continue
		}
		if level == LossAggressive && isMixWarning(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("mix: %s, %d lines dropped", level, dropped)
	res := enforceCritical(m, raw, compact, dropped, notes)
	return res, true
}
