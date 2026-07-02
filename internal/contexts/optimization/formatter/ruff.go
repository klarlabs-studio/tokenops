package formatter

import (
	"fmt"
	"regexp"
	"strings"
)

// Ruff compresses `ruff check` default output. Each finding is a line of the
// shape "path/file.py:10:5: E501 Line too long (90 > 88)"; findings are the
// signal an agent acts on and always survive. ruff closes with a "Found N
// errors." summary and, when fixes are available, an advisory
// "[*] N fixable with the `--fix` option." line; it may also echo a source
// snippet with a "^" caret beneath a finding.
//
// ruff output is mostly signal, so compression is modest: the safe drops are
// the "[*] ... fixable" advisory and the source-snippet/caret echo. Every
// finding line and the "Found N errors" summary are kept.
//
// Non-ruff output that reaches this formatter is handed to the generic scrub,
// so the formatter is never destructive on output it does not model.
type Ruff struct{}

// NewRuff returns the ruff formatter.
func NewRuff() *Ruff { return &Ruff{} }

// Command reports the ruff command token.
func (r *Ruff) Command() string { return "ruff" }

// ruffFinding matches a ruff finding line: a Python source path with
// "line:col:" position followed by a rule code (e.g. E501, F401, W291).
var ruffFinding = regexp.MustCompile(`\.py:\d+:\d+: [A-Z]+\d+`)

// ruffSummary matches the terminal "Found N errors." summary line.
var ruffSummary = regexp.MustCompile(`^Found \d+ error`)

// CriticalLine treats every finding line and the "Found N errors" summary as
// critical. The "[*] ... fixable" advisory and source-snippet echo are noise.
func (r *Ruff) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	switch {
	case ruffFinding.MatchString(s),
		ruffSummary.MatchString(s):
		return true
	}
	return false
}

// looksLikeRuff reports whether b resembles `ruff check` output.
func looksLikeRuff(b []byte) bool {
	s := string(b)
	return ruffFinding.MatchString(s) ||
		strings.Contains(s, "Found ") ||
		strings.Contains(s, "ruff")
}

// isRuffFixable reports the advisory "[*] N fixable with the `--fix` option."
// line, dropped at Balanced+.
func isRuffFixable(s string) bool {
	return strings.HasPrefix(s, "[*]") && strings.Contains(s, "fixable")
}

// isRuffSnippet reports whether a non-finding, non-summary line is a
// source-snippet echo: an indented code line beneath a finding or a "^" caret
// line (only carets and spaces).
func isRuffSnippet(raw, trimmed string) bool {
	if trimmed == "" {
		return false
	}
	if strings.Trim(trimmed, "^ \t") == "" {
		return true
	}
	return len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t')
}

// Format compresses ruff output; non-ruff output falls back to generic.
func (r *Ruff) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeRuff(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "ruff: non-ruff output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(r, raw, scrubbed, 0, "ruff: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically: drop the "[*] ... fixable"
	// advisory and the source-snippet echo. Keep all findings and the summary.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if r.CriticalLine(s) {
			kept = append(kept, line)
			continue
		}
		if isRuffFixable(s) || isRuffSnippet(line, s) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("ruff: %s, %d lines dropped", level, dropped)
	res := enforceCritical(r, raw, compact, dropped, notes)
	return res, true
}
