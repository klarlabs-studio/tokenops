package formatter

import (
	"fmt"
	"strings"
)

// Yarn compresses the output of `yarn install` (and, via the pnpm alias,
// `pnpm install`). The error/summary signal — any resolver error, an
// "ERR_" code, or a "Couldn't find" dependency-resolution failure — and the
// "Done in Xs" completion line are the state an agent acts on. The errors are
// always kept; the completion line survives Balanced+.
//
// The phase markers yarn prints ("[1/4] Resolving packages…" through
// "[4/4] Building fresh packages…"), the pnpm "Progress: resolved …" and
// "Packages: +N" counters, the per-package "+ pkg@1.2.3" / "- pkg@1.2.3"
// listing lines, the "success Saved lockfile." notice, and the advisory
// "warning …" lines carry no state an agent needs and are stripped at
// Balanced and above.
//
// Output that does not resemble yarn/pnpm is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not model.
type Yarn struct{}

// NewYarn returns the yarn formatter.
func NewYarn() *Yarn { return &Yarn{} }

// Command reports the yarn command token.
func (y *Yarn) Command() string { return "yarn" }

// Aliases registers the formatter under "pnpm" so pnpm output routes here
// too. Command() remains the canonical token.
func (y *Yarn) Aliases() []string { return []string{"pnpm"} }

// CriticalLine treats resolver error and summary signal as critical: any line
// carrying an error (case-insensitive "error"), an "ERR_" code, or a
// "Couldn't find" dependency-resolution failure. The "Done in Xs" completion
// line and advisory "warning" lines are never critical.
func (y *Yarn) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	switch {
	case strings.Contains(lower, "error"),
		strings.Contains(t, "ERR_"),
		strings.Contains(t, "Couldn't find"):
		return true
	}
	return false
}

// looksLikeYarn reports whether b resembles yarn/pnpm output.
func looksLikeYarn(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "yarn install") ||
		strings.Contains(s, "Resolving packages") ||
		strings.Contains(s, "Fetching packages") ||
		strings.Contains(s, "Linking dependencies") ||
		strings.Contains(s, "Done in") ||
		strings.Contains(s, "Packages:") ||
		strings.Contains(s, "Progress:") ||
		strings.Contains(s, "yarn") ||
		strings.Contains(s, "pnpm")
}

// isYarnNoise reports the phase/progress/listing lines that carry no state
// and are safe to drop at Balanced+: yarn phase markers, the lockfile-saved
// notice, the pnpm progress/package counters, the per-package listing lines,
// and advisory warnings. Critical lines are handled before this is called.
func isYarnNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "[1/4]"),
		strings.HasPrefix(t, "[2/4]"),
		strings.HasPrefix(t, "[3/4]"),
		strings.HasPrefix(t, "[4/4]"),
		strings.HasPrefix(t, "success Saved lockfile"),
		strings.HasPrefix(t, "Resolving packages"),
		strings.HasPrefix(t, "Fetching packages"),
		strings.HasPrefix(t, "Linking dependencies"),
		strings.HasPrefix(t, "Building fresh packages"),
		strings.HasPrefix(t, "Progress: resolved"),
		strings.HasPrefix(t, "Packages: +"),
		strings.HasPrefix(t, "warning "),
		strings.HasPrefix(t, "+ "),
		strings.HasPrefix(t, "- "):
		return true
	}
	return false
}

// Format compresses yarn/pnpm output; non-yarn output falls back to generic.
func (y *Yarn) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeYarn(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "yarn: non-yarn output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(y, raw, scrubbed, 0, "yarn: conservative scrub")
		return res, true
	}

	// Balanced and Aggressive behave identically here: the phase/progress
	// noise is already stripped at Balanced, leaving nothing further for
	// Aggressive to collapse, so both produce the same (monotonic) output.
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if y.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isYarnNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("yarn: %s, %d lines dropped", level, dropped)
	res := enforceCritical(y, raw, compact, dropped, notes)
	return res, true
}
