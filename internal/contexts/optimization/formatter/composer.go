package formatter

import (
	"fmt"
	"strings"
)

// Composer compresses the output of `composer install` / `composer update` and
// related commands. Any error (a dependency-resolution failure, "could not be
// resolved", a "Problem" report, or a "requires" conflict) is the signal an
// agent acts on and is always kept, together with the package-count/funding
// summary when present.
//
// The per-package operation chatter ("  - Installing symfony/console …",
// "  - Downloading …", "  - Extracting archive"), the repository-loading
// preamble ("Loading composer repositories …"), and the "Reading …" cache
// chatter carry no state and are stripped at Balanced and above.
//
// Output that does not resemble composer is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Composer struct{}

// NewComposer returns the composer formatter.
func NewComposer() *Composer { return &Composer{} }

// Command reports the composer command token.
func (c *Composer) Command() string { return "composer" }

// CriticalLine treats resolution-failure signal as critical, kept tight: a
// line is critical iff it contains (case-insensitive) "error", "could not",
// "problem", or "conflict". The "Generating autoload files" progress line and
// the per-package operation chatter are never critical.
func (c *Composer) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	switch {
	case strings.Contains(lower, "error"),
		strings.Contains(lower, "could not"),
		strings.Contains(lower, "problem"),
		strings.Contains(lower, "conflict"):
		return true
	}
	return false
}

// looksLikeComposer reports whether b resembles composer output.
func looksLikeComposer(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "composer") ||
		strings.Contains(s, "Installing ") ||
		strings.Contains(s, "Lock file operations") ||
		strings.Contains(s, "Loading composer") ||
		strings.Contains(s, "autoload")
}

// isComposerNoise reports the per-package/preamble lines that carry no state
// and are safe to drop at Balanced+: the "  - Installing "/"  - Downloading
// "/"  - Extracting " operation chatter, the "Loading composer repositories"
// preamble, and the "Reading " cache chatter.
func isComposerNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "- Installing "),
		strings.HasPrefix(t, "- Downloading "),
		strings.HasPrefix(t, "- Extracting "),
		strings.HasPrefix(t, "Loading composer repositories"),
		strings.HasPrefix(t, "Reading "):
		return true
	}
	return false
}

// Format compresses composer output; non-composer output falls back to
// generic.
func (c *Composer) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeComposer(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "composer: non-composer output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(c, raw, scrubbed, 0, "composer: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if c.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isComposerNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("composer: %s, %d lines dropped", level, dropped)
	res := enforceCritical(c, raw, compact, dropped, notes)
	return res, true
}
