package formatter

import (
	"fmt"
	"strings"
)

// Brew compresses the output of `brew install` (Homebrew) and related
// commands. The "==> Pouring …" line (the actual bottle-install action), the
// "🍺  /opt/homebrew/Cellar/…" success summary, and any error ("Error:") are
// the signal an agent acts on and are always kept.
//
// The "==> Downloading …", "==> Fetching …", and "==> Installing dependencies
// …" progress banners carry no state and are stripped at Balanced and above;
// "==> Pouring" is deliberately preserved because it names the install. At
// Aggressive the advisory "Warning: …" lines (e.g. "already installed") are
// additionally dropped.
//
// Output that does not resemble Homebrew is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Brew struct{}

// NewBrew returns the brew formatter.
func NewBrew() *Brew { return &Brew{} }

// Command reports the brew command token.
func (b *Brew) Command() string { return "brew" }

// CriticalLine treats error and install-action signal as critical: any error
// line ("Error:" prefix), the "🍺" success summary, and the "==> Pouring"
// bottle-install line the agent cares about. Advisory "Warning:" lines
// (including "already installed") are never critical.
func (b *Brew) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	return strings.HasPrefix(t, "Error") ||
		strings.Contains(t, "🍺") ||
		strings.Contains(t, "Pouring")
}

// looksLikeBrew reports whether b resembles Homebrew output.
func looksLikeBrew(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "==> ") ||
		strings.Contains(s, "brew") ||
		strings.Contains(s, "Pouring") ||
		strings.Contains(s, "Homebrew") ||
		strings.Contains(s, "🍺") ||
		strings.Contains(s, "Cellar")
}

// isBrewNoise reports the download/fetch/dependency progress banners that
// carry no state and are safe to drop at Balanced+. "==> Pouring" is handled
// by CriticalLine and is never dropped.
func isBrewNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "==> Downloading"),
		strings.HasPrefix(t, "==> Fetching"),
		strings.HasPrefix(t, "==> Installing dependencies"):
		return true
	}
	return false
}

// Format compresses brew output; non-brew output falls back to generic.
func (b *Brew) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeBrew(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "brew: non-brew output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(b, raw, scrubbed, 0, "brew: conservative scrub")
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
		if isBrewNoise(t) || t == "" {
			dropped++
			continue
		}
		// Aggressive additionally sheds the advisory "Warning:" lines that
		// Balanced keeps.
		if level == LossAggressive && strings.HasPrefix(t, "Warning:") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("brew: %s, %d lines dropped", level, dropped)
	res := enforceCritical(b, raw, compact, dropped, notes)
	return res, true
}
