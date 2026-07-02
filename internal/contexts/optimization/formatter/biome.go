package formatter

import (
	"fmt"
	"regexp"
	"strings"
)

// Biome compresses `biome check` output. Biome prints a "Checking N files"
// progress line, one diagnostic block per problem — a header line
// ("path:line:col rule/path  FIXABLE  ...") followed by a "✖"/"×" marker, a
// source code frame with a caret/pointer, and advisory notes — and a terminal
// "Checked N files ... Found M errors." summary.
//
// The diagnostic header lines, error-marker lines, and the summary are the
// signal an agent acts on and are always kept. The code-frame echo (indented
// source and caret/pointer lines) and the progress line are noise reproduced
// from the user's own files, so they are dropped at Balanced+.
//
// Non-biome output that reaches this formatter is handed to the generic
// scrub, so the formatter is never destructive on output it does not model.
type Biome struct{}

// NewBiome returns the biome formatter.
func NewBiome() *Biome { return &Biome{} }

// Command reports the biome command token.
func (b *Biome) Command() string { return "biome" }

// biomeHeader matches a diagnostic header position: a JS/TS source path
// followed by a "line:col" position.
var biomeHeader = regexp.MustCompile(`\.(?:ts|js|tsx|jsx|mts|cts):\d+:\d+`)

// CriticalLine treats error-marker lines, diagnostic header lines, and the
// "Found N errors" summary as critical. Warnings are advisory (not critical).
func (b *Biome) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	switch {
	case isBiomeSummary(s),
		isBiomeErrorMarker(s),
		isBiomeDiagnosticHeader(s):
		return true
	}
	return false
}

// looksLikeBiome reports whether b resembles `biome check` output.
func looksLikeBiome(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Checked ") ||
		strings.Contains(s, "biome") ||
		strings.Contains(s, "lint/") ||
		strings.Contains(s, "Found ") ||
		strings.Contains(s, "✖")
}

// isBiomeErrorMarker reports an error line: one bearing the "✖"/"×" glyph or
// the word "error".
func isBiomeErrorMarker(s string) bool {
	return strings.Contains(s, "✖") ||
		strings.Contains(s, "×") ||
		strings.Contains(s, " error") ||
		strings.Contains(s, "error[")
}

// isBiomeDiagnosticHeader reports a diagnostic header line: a "path:line:col"
// position followed by a "lint/" rule path.
func isBiomeDiagnosticHeader(s string) bool {
	return biomeHeader.MatchString(s) && strings.Contains(s, "lint/")
}

// isBiomeSummary reports the terminal "Found N errors" summary. Warning
// summaries ("Found N warnings") are advisory and not matched here.
func isBiomeSummary(s string) bool {
	return strings.HasPrefix(s, "Found ") && strings.Contains(s, "error")
}

// isBiomeProgress reports the "Checking N files" progress line. It is
// distinct from the "Checked N files ..." summary.
func isBiomeProgress(s string) bool {
	return strings.HasPrefix(s, "Checking ")
}

// isBiomeIndented reports whether a line is indented (a code-frame source or
// caret/pointer echo) rather than a flush-left header or summary.
func isBiomeIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// Format compresses biome output; non-biome output falls back to generic.
func (b *Biome) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeBiome(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "biome: non-biome output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(b, raw, scrubbed, 0, "biome: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically: drop the progress line and
	// the code-frame echo (indented source and caret/pointer lines that are
	// not diagnostic headers), keeping diagnostics and the summary.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		switch {
		case b.CriticalLine(s):
			kept = append(kept, line)
		case s == "":
			dropped++
		case isBiomeProgress(s):
			dropped++
		case isBiomeIndented(line):
			dropped++ // code-frame source or caret/pointer echo
		default:
			kept = append(kept, line)
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("biome: %s, %d lines dropped", level, dropped)
	res := enforceCritical(b, raw, compact, dropped, notes)
	return res, true
}
