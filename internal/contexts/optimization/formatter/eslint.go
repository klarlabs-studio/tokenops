package formatter

import (
	"fmt"
	"strings"
)

// ESLint compresses `eslint` stylish output. The stylish reporter groups
// problems by file: a non-indented file-path header, then indented
// "line:col  severity  message  rule" problem lines, then a terminal
// "✖ N problems (E errors, W warnings)" summary.
//
// eslint output is almost all signal with little noise, so compression is
// modest. Errors and the summary are what an agent acts on and always
// survive. Warnings are advisory and dropped at Balanced+; a file header
// left with no remaining problems after warning removal is dropped with it.
//
// File-path headers are preserved structurally (kept whenever they head at
// least one surviving error) rather than enforced as critical, because the
// warning-only header is intentionally dropped at Balanced+ and the
// critical-survival guard permits no dropped critical line.
//
// Non-eslint output that reaches this formatter is handed to the generic
// scrub, so the formatter is never destructive on output it does not model.
type ESLint struct{}

// NewESLint returns the eslint formatter.
func NewESLint() *ESLint { return &ESLint{} }

// Command reports the eslint command token.
func (e *ESLint) Command() string { return "eslint" }

// CriticalLine treats error problem lines and the summary as critical.
// Warnings are advisory (not critical). File-path headers are kept
// structurally by Format, not enforced here (see the type comment).
func (e *ESLint) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	switch {
	case isESLintSummary(s),
		isESLintErrorLine(s):
		return true
	}
	return false
}

// looksLikeESLint reports whether b resembles `eslint` stylish output.
func looksLikeESLint(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "problem") ||
		strings.Contains(s, "  error  ") ||
		strings.Contains(s, "eslint") ||
		strings.Contains(s, "✖")
}

// isESLintIndented reports whether a line is an indented problem line rather
// than a flush-left file-path header.
func isESLintIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// eslintFileExts are the source extensions an eslint file-path header ends in.
var eslintFileExts = []string{".js", ".ts", ".jsx", ".tsx", ".vue"}

// isESLintFileHeader reports whether the trimmed line is a file-path header
// (a path ending in a JS/TS/Vue extension). The caller must confirm the line
// is not indented before treating it as a header.
func isESLintFileHeader(s string) bool {
	for _, ext := range eslintFileExts {
		if strings.HasSuffix(s, ext) {
			return true
		}
	}
	return false
}

// isESLintErrorLine reports an error problem line (not a warning).
func isESLintErrorLine(s string) bool {
	return strings.Contains(s, "  error  ") || strings.Contains(s, "  error ")
}

// isESLintWarningLine reports a warning problem line (advisory).
func isESLintWarningLine(s string) bool {
	return strings.Contains(s, "  warning  ") || strings.Contains(s, "  warning ")
}

// isESLintSummary reports the terminal "N problems" summary line.
func isESLintSummary(s string) bool {
	return strings.HasPrefix(s, "✖") ||
		strings.Contains(s, " problems") ||
		strings.Contains(s, " problem ")
}

// Format compresses eslint output; non-eslint output falls back to generic.
func (e *ESLint) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeESLint(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "eslint: non-eslint output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(e, raw, scrubbed, 0, "eslint: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically: drop advisory warnings and
	// any file header left with no surviving problems.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	var header string
	haveHeader := false
	var group []string // surviving problem lines under the current header

	flush := func() {
		if !haveHeader {
			return
		}
		if len(group) > 0 {
			kept = append(kept, header)
			kept = append(kept, group...)
		} else {
			dropped++ // now-empty header
		}
		haveHeader = false
		header = ""
		group = nil
	}

	for _, line := range lines {
		s := strings.TrimSpace(line)
		switch {
		case !isESLintIndented(line) && isESLintFileHeader(s):
			flush()
			header = line
			haveHeader = true
		case isESLintWarningLine(s):
			dropped++ // advisory
		case isESLintErrorLine(s):
			if haveHeader {
				group = append(group, line)
			} else {
				kept = append(kept, line)
			}
		case isESLintSummary(s):
			flush()
			kept = append(kept, line)
		case s == "":
			dropped++
		default:
			// Any other content line: retain it (outside header grouping).
			kept = append(kept, line)
		}
	}
	flush()

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("eslint: %s, %d lines dropped", level, dropped)
	res := enforceCritical(e, raw, compact, dropped, notes)
	return res, true
}
