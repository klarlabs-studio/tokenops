package formatter

import (
	"fmt"
	"regexp"
	"strings"
)

// Rubocop compresses `rubocop` output. Rubocop prints an "Inspecting N files"
// banner, a per-file progress line of dots and severity letters ("..C..W."),
// an "Offenses:" section whose findings each carry a source snippet and a
// "^^^" caret echo, and a terminal "N files inspected, M offenses detected"
// summary.
//
// The finding lines and the summary are the signal an agent acts on and are
// always kept. The inspection banner, the pure progress line, and the source
// snippet + caret echo under each finding are noise reproduced from the
// user's own files, so they are dropped at Balanced+.
//
// Non-rubocop output that reaches this formatter is handed to the generic
// scrub, so the formatter is never destructive on output it does not model.
type Rubocop struct{}

// NewRubocop returns the rubocop formatter.
func NewRubocop() *Rubocop { return &Rubocop{} }

// Command reports the rubocop command token.
func (r *Rubocop) Command() string { return "rubocop" }

// rubocopFinding matches an offense finding line: a Ruby source path, a
// "line:col" position, and a single-letter severity code ("C:" convention,
// "W:" warning, "E:" error, "F:" fatal, "R:" refactor).
var rubocopFinding = regexp.MustCompile(`\.rb:\d+:\d+: [A-Z]: `)

// CriticalLine treats offense finding lines and the summary as critical.
// Source snippets and caret echoes are noise and never critical.
func (r *Rubocop) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	switch {
	case isRubocopSummary(s),
		isRubocopFinding(s):
		return true
	}
	return false
}

// looksLikeRubocop reports whether b resembles `rubocop` output.
func looksLikeRubocop(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Inspecting") ||
		strings.Contains(s, "offenses") ||
		strings.Contains(s, "Offenses:") ||
		strings.Contains(s, "rubocop") ||
		strings.Contains(s, "files inspected")
}

// isRubocopFinding reports an offense finding line.
func isRubocopFinding(s string) bool {
	return rubocopFinding.MatchString(s)
}

// isRubocopSummary reports the terminal "N files inspected, M offenses
// detected" line.
func isRubocopSummary(s string) bool {
	return strings.Contains(s, "offense")
}

// isRubocopInspecting reports the "Inspecting N files" banner.
func isRubocopInspecting(s string) bool {
	return strings.HasPrefix(s, "Inspecting")
}

// rubocopProgressChars are the characters a rubocop progress line is built
// from: dots for clean files and severity letters for offending ones.
const rubocopProgressChars = ".CWEFR "

// isRubocopProgress reports the pure progress line ("..C..W."): a non-empty
// line made only of dots, severity letters, and spaces.
func isRubocopProgress(s string) bool {
	if s == "" {
		return false
	}
	return strings.Trim(s, rubocopProgressChars) == ""
}

// isRubocopIndented reports whether a line is indented (a source snippet or
// caret echo under a finding) rather than a flush-left finding or header.
func isRubocopIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// Format compresses rubocop output; non-rubocop output falls back to generic.
func (r *Rubocop) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeRubocop(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "rubocop: non-rubocop output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(r, raw, scrubbed, 0, "rubocop: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically: drop the inspection banner,
	// the progress line, and the source snippet + caret echo under findings.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		switch {
		case r.CriticalLine(s):
			kept = append(kept, line)
		case s == "":
			dropped++
		case isRubocopInspecting(s):
			dropped++
		case isRubocopProgress(s):
			dropped++
		case isRubocopIndented(line):
			dropped++ // source snippet or caret echo
		default:
			kept = append(kept, line) // e.g. the "Offenses:" header
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("rubocop: %s, %d lines dropped", level, dropped)
	res := enforceCritical(r, raw, compact, dropped, notes)
	return res, true
}
