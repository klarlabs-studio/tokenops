package formatter

import (
	"fmt"
	"strings"
)

// Prettier compresses `prettier --check` output. Prettier prints a "Checking
// formatting..." progress line, one "[warn] path" line per file that is not
// formatted, and a terminal summary — either "[warn] Code style issues found
// in N files. Run Prettier with --write to fix." or "All matched files use
// Prettier code style!".
//
// The per-file "[warn]" lines, any "[error]" lines, and the summary are the
// signal an agent acts on and are always kept. The only noise is the
// "Checking formatting..." progress line, dropped at Balanced+.
//
// Non-prettier output that reaches this formatter is handed to the generic
// scrub, so the formatter is never destructive on output it does not model.
type Prettier struct{}

// NewPrettier returns the prettier formatter.
func NewPrettier() *Prettier { return &Prettier{} }

// Command reports the prettier command token.
func (p *Prettier) Command() string { return "prettier" }

// CriticalLine treats per-file "[warn]" lines, "[error]" lines, and the
// summary as critical.
func (p *Prettier) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	switch {
	case isPrettierSummary(s),
		isPrettierError(s),
		isPrettierWarnFile(s):
		return true
	}
	return false
}

// looksLikePrettier reports whether b resembles `prettier --check` output.
func looksLikePrettier(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "[warn]") ||
		strings.Contains(s, "Checking formatting") ||
		strings.Contains(s, "prettier") ||
		strings.Contains(s, "Prettier code style") ||
		strings.Contains(s, "[error]")
}

// isPrettierSummary reports the terminal summary line, in either the
// issues-found or all-clean form.
func isPrettierSummary(s string) bool {
	return strings.Contains(s, "Code style issues found") ||
		strings.Contains(s, "All matched files")
}

// isPrettierError reports an "[error]" line.
func isPrettierError(s string) bool {
	return strings.HasPrefix(s, "[error]")
}

// isPrettierWarnFile reports a per-file "[warn]" line (i.e. a "[warn]" line
// that is not the summary).
func isPrettierWarnFile(s string) bool {
	return strings.HasPrefix(s, "[warn]") && !isPrettierSummary(s)
}

// isPrettierProgress reports the "Checking formatting..." progress line.
func isPrettierProgress(s string) bool {
	return strings.HasPrefix(s, "Checking formatting")
}

// Format compresses prettier output; non-prettier output falls back to generic.
func (p *Prettier) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikePrettier(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "prettier: non-prettier output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(p, raw, scrubbed, 0, "prettier: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically: drop the progress line and
	// keep every [warn]/[error]/summary line.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		switch {
		case p.CriticalLine(s):
			kept = append(kept, line)
		case s == "":
			dropped++
		case isPrettierProgress(s):
			dropped++
		default:
			kept = append(kept, line)
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("prettier: %s, %d lines dropped", level, dropped)
	res := enforceCritical(p, raw, compact, dropped, notes)
	return res, true
}
