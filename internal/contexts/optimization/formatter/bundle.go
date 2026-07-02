package formatter

import (
	"fmt"
	"strings"
)

// Bundle compresses the output of `bundle install` and related commands. The
// completion summary ("Bundle complete! …") and any error (a dependency
// conflict, "Could not find gem …", or a Bundler resolution failure) are the
// signal an agent acts on and are always kept.
//
// The per-gem resolution chatter ("Using rails 7.1.0", "Fetching nokogiri
// 1.16.0", "Installing nokogiri 1.16.0 with native extensions") carries no
// state beyond the completion summary and is stripped at Balanced and above.
//
// Output that does not resemble bundle is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Bundle struct{}

// NewBundle returns the bundle formatter.
func NewBundle() *Bundle { return &Bundle{} }

// Command reports the bundle command token.
func (b *Bundle) Command() string { return "bundle" }

// CriticalLine treats error and completion signal as critical: the "Bundle
// complete!" summary, and any line reporting an error ("error", "Could not
// find", "conflict", "Bundler could not"). The per-gem "Using "/"Fetching
// "/"Installing " chatter is never critical.
func (b *Bundle) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(t, "Bundle complete!"),
		strings.Contains(lower, "error"),
		strings.Contains(t, "Could not find"),
		strings.Contains(lower, "conflict"),
		strings.Contains(t, "Bundler could not"):
		return true
	}
	return false
}

// looksLikeBundle reports whether b resembles bundle output.
func looksLikeBundle(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "Fetching gem") ||
		strings.Contains(s, "Using ") ||
		strings.Contains(s, "Bundle complete") ||
		strings.Contains(s, "bundle") ||
		strings.Contains(s, "Installing ")
}

// isBundleNoise reports the per-gem resolution lines that carry no state
// beyond the completion summary and are safe to drop at Balanced+: the
// "Using "/"Fetching "/"Installing " chatter.
func isBundleNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Using "),
		strings.HasPrefix(t, "Fetching "),
		strings.HasPrefix(t, "Installing "):
		return true
	}
	return false
}

// Format compresses bundle output; non-bundle output falls back to generic.
func (b *Bundle) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeBundle(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "bundle: non-bundle output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(b, raw, scrubbed, 0, "bundle: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if b.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isBundleNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("bundle: %s, %d lines dropped", level, dropped)
	res := enforceCritical(b, raw, compact, dropped, notes)
	return res, true
}
