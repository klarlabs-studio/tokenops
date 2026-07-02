package formatter

import (
	"fmt"
	"regexp"
	"strings"
)

// GolangciLint compresses `golangci-lint` default output. Each finding is a
// line of the shape "path/file.go:12:5: message (linter)"; findings are the
// signal an agent acts on and always survive. golangci-lint interleaves each
// finding with a source-snippet echo (the offending code line plus a "^"
// caret line) and closes with an "N issues:" summary followed by a per-linter
// count breakdown ("* revive: 1").
//
// golangci-lint output is mostly signal, so compression is modest: the safe
// drops are the source-snippet echo and the per-linter count breakdown. Every
// finding line and the "N issues:" summary are kept.
//
// Non-golangci output that reaches this formatter is handed to the generic
// scrub, so the formatter is never destructive on output it does not model.
type GolangciLint struct{}

// NewGolangciLint returns the golangci-lint formatter.
func NewGolangciLint() *GolangciLint { return &GolangciLint{} }

// Command reports the golangci-lint command token.
func (g *GolangciLint) Command() string { return "golangci-lint" }

// golangciFinding matches a golangci-lint finding line: a Go source path with
// "line:col:" position and a following ": " before the message.
var golangciFinding = regexp.MustCompile(`\.go:\d+:\d*:? `)

// golangciIssuesSummary matches the terminal "N issues:" summary line.
var golangciIssuesSummary = regexp.MustCompile(`^\d+ issues:`)

// CriticalLine treats every finding line and the "N issues:" summary as
// critical. Source-snippet echo lines and the per-linter count breakdown are
// noise.
func (g *GolangciLint) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	switch {
	case golangciFinding.MatchString(s),
		golangciIssuesSummary.MatchString(s):
		return true
	}
	return false
}

// looksLikeGolangci reports whether b resembles `golangci-lint` output.
func looksLikeGolangci(b []byte) bool {
	s := string(b)
	return golangciFinding.MatchString(s) ||
		strings.Contains(s, "issues:") ||
		strings.Contains(s, "golangci")
}

// isGolangciSnippet reports whether a non-finding, non-summary line is a
// source-snippet echo: the indented code line beneath a finding or the "^"
// caret line (only "^" and spaces).
func isGolangciSnippet(raw, trimmed string) bool {
	if trimmed == "" {
		return false
	}
	// A caret line contains only carets and spaces.
	if strings.Trim(trimmed, "^ \t") == "" {
		return true
	}
	// An indented echo line: leading whitespace and not a finding/summary.
	return len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t')
}

// isGolangciCount reports the per-linter count breakdown lines ("* revive: 1").
func isGolangciCount(s string) bool {
	return strings.HasPrefix(s, "* ")
}

// Format compresses golangci-lint output; non-golangci output falls back to
// generic.
func (g *GolangciLint) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeGolangci(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "golangci-lint: non-golangci output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(g, raw, scrubbed, 0, "golangci-lint: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically: drop the source-snippet echo
	// and the per-linter count breakdown. Keep all findings and the summary.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if g.CriticalLine(s) {
			kept = append(kept, line)
			continue
		}
		if isGolangciCount(s) || isGolangciSnippet(line, s) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("golangci-lint: %s, %d lines dropped", level, dropped)
	res := enforceCritical(g, raw, compact, dropped, notes)
	return res, true
}
