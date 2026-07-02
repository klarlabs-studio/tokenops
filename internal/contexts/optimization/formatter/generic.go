package formatter

import (
	"regexp"
	"strings"
)

// Generic is the command-agnostic formatter. It never drops semantic
// content: it strips ANSI escapes, trailing whitespace, collapses runs of
// blank lines, and removes exact consecutive duplicate lines. It treats no
// line as critical (there is no reliable cross-command signal), so its
// output is pure-noise reduction and safe for any command at any level.
type Generic struct{}

// NewGeneric returns the generic formatter.
func NewGeneric() *Generic { return &Generic{} }

// Command reports the sentinel token for the generic fallback.
func (g *Generic) Command() string { return "" }

// CriticalLine always reports false: the generic formatter makes no
// command-specific claim about which lines matter, so it only removes lines
// that are provably redundant (duplicates, blanks) rather than classifying
// content.
func (g *Generic) CriticalLine(string) bool { return false }

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// Format applies the noise-reduction rules. Level is accepted for
// interface symmetry but the generic formatter behaves identically at
// every level — it has no command-specific noise to escalate on.
func (g *Generic) Format(raw []byte, _ LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	compact, dropped := scrub(raw)
	return Result{
		Compact:      compact,
		BytesBefore:  len(raw),
		BytesAfter:   len(compact),
		LinesDropped: dropped,
		CriticalKept: true,
		Notes:        "generic noise scrub",
	}, true
}

// scrub is the shared noise reducer used by Generic and reused by
// command formatters before they apply their own rules. It removes ANSI
// escapes, trims trailing whitespace, drops exact consecutive duplicate
// lines, and collapses 2+ blank lines to one. It reports how many source
// lines were removed.
func scrub(raw []byte) ([]byte, int) {
	text := ansiEscape.ReplaceAllString(string(raw), "")
	lines := strings.Split(text, "\n")

	out := make([]string, 0, len(lines))
	dropped := 0
	prevBlank := false
	prevLine := "\x00sentinel" // impossible first value
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "" {
			if prevBlank {
				dropped++
				continue
			}
			prevBlank = true
			out = append(out, "")
			prevLine = ""
			continue
		}
		if trimmed == prevLine {
			dropped++
			continue
		}
		prevBlank = false
		prevLine = trimmed
		out = append(out, trimmed)
	}
	// Trim a leading/trailing blank the collapsing may leave behind.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return []byte(strings.Join(out, "\n")), dropped
}
