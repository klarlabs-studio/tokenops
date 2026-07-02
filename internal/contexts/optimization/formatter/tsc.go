package formatter

import (
	"fmt"
	"strings"
)

// TSC compresses `tsc` (TypeScript compiler) output. Diagnostics and the
// terminal "Found N errors" summary are the signal an agent acts on and are
// always kept. tsc output is almost all signal with little noise, so
// compression is modest: the only safe drops are the performance/stats block
// that `tsc --extendedDiagnostics` prints and progress chatter.
//
// Non-tsc output that reaches this formatter is handed to the generic scrub,
// so the formatter is never destructive on output it does not model.
type TSC struct{}

// NewTSC returns the tsc formatter.
func NewTSC() *TSC { return &TSC{} }

// Command reports the tsc command token.
func (t *TSC) Command() string { return "tsc" }

// tscStatsPrefixes are the extendedDiagnostics performance/stats lines that
// carry no diagnostic signal and are safe to drop at Balanced+.
var tscStatsPrefixes = []string{
	"Files:",
	"Lines:",
	"Nodes:",
	"Identifiers:",
	"Symbols:",
	"Types:",
	"Instantiations:",
	"Memory used:",
	"Assignability cache size:",
	"I/O Read:",
	"Parse time:",
	"Bind time:",
	"Check time:",
	"Emit time:",
	"Total time:",
}

// CriticalLine treats every diagnostic line as critical: the tsc error shape
// "path.ts(line,col): error TSxxxx: message" and the terminal "Found N
// errors" summary. The extendedDiagnostics stats block and blanks are noise.
func (t *TSC) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	switch {
	case strings.Contains(s, "): error TS"),
		strings.HasPrefix(s, "Found ") && strings.Contains(s, "error"):
		return true
	}
	return false
}

// looksLikeTSC reports whether b resembles `tsc` output.
func looksLikeTSC(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "): error TS") ||
		strings.Contains(s, "error TS") ||
		strings.Contains(s, "Found ") ||
		strings.Contains(s, "tsc")
}

// isTSCStats reports the extendedDiagnostics performance/stats lines and
// "Compiling..." progress chatter, dropped at Balanced+.
func isTSCStats(s string) bool {
	if strings.HasPrefix(s, "Compiling") {
		return true
	}
	for _, p := range tscStatsPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// Format compresses tsc output; non-tsc output falls back to generic.
func (t *TSC) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeTSC(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "tsc: non-tsc output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(t, raw, scrubbed, 0, "tsc: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically: tsc has no safe further
	// collapse beyond dropping the stats/progress block.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if t.CriticalLine(s) {
			kept = append(kept, line)
			continue
		}
		if isTSCStats(s) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("tsc: %s, %d lines dropped", level, dropped)
	res := enforceCritical(t, raw, compact, dropped, notes)
	return res, true
}
