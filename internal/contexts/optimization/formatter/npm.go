package formatter

import (
	"fmt"
	"strings"
)

// NPM compresses the output of `npm install` / `npm ci` and related
// commands. The change-summary lines ("added N packages", "removed N
// packages"), any error ("npm error" / "npm ERR!"), and audit
// vulnerability lines are the signal an agent acts on and are always kept.
//
// The per-request fetch chatter ("npm http fetch …"), timing traces, reify
// progress, advisory notices, and progress-bar/spinner frames are noise
// stripped at Balanced and above. The per-package deprecation warnings
// ("npm warn deprecated foo@1.2.3: …") are collapsed into a single count so
// a noisy install shrinks to its state-bearing summary — the collapse uses
// the same size guard as the git formatter's untracked-file block, so it
// never produces output larger than the lines it replaces.
//
// Output that does not resemble npm is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type NPM struct{}

// NewNPM returns the npm formatter.
func NewNPM() *NPM { return &NPM{} }

// Command reports the npm command token.
func (n *NPM) Command() string { return "npm" }

// CriticalLine treats error and change-summary signal as critical: any line
// carrying an npm error ("npm error" / "npm ERR!"), any audit vulnerability
// line, and the install change-summary lines ("added N packages", "removed N
// packages", "changed N packages"). Advisory warnings, notices, and funding
// chatter are never critical.
func (n *NPM) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.Contains(t, "npm error"),
		strings.Contains(t, "npm ERR!"),
		strings.Contains(t, "vulnerabilit"),
		strings.HasPrefix(t, "added "),
		strings.HasPrefix(t, "removed "),
		strings.HasPrefix(t, "changed "):
		return true
	}
	return false
}

// looksLikeNPM reports whether b resembles npm output.
func looksLikeNPM(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "npm ") ||
		strings.Contains(s, "added ") ||
		strings.Contains(s, "packages in")
}

// isNPMDeprecation reports the per-package deprecation warning lines, which
// are collapsed into a single count at Balanced+.
func isNPMDeprecation(t string) bool {
	return strings.HasPrefix(t, "npm warn deprecated") ||
		strings.HasPrefix(t, "npm WARN deprecated")
}

// isNPMNoise reports the progress/advisory lines that carry no state and are
// safe to drop at Balanced+: fetch chatter, timing traces, reify progress,
// and advisory notices. Deprecation warnings are handled separately.
func isNPMNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "npm http"),
		strings.HasPrefix(t, "npm timing"),
		strings.HasPrefix(t, "npm notice"),
		strings.HasPrefix(t, "reify:"):
		return true
	}
	return isNPMProgressLine(t)
}

// isNPMProgressLine reports lines composed solely of the glyphs npm uses to
// draw progress bars and spinners (braille spinner frames, block-element
// bars, and the ⸨ ⸩ progress brackets) plus whitespace. Requiring the whole
// line to be progress glyphs keeps the check from ever eating a content line.
func isNPMProgressLine(t string) bool {
	if t == "" {
		return false
	}
	hasGlyph := false
	for _, r := range t {
		switch {
		case r >= 0x2800 && r <= 0x28FF: // braille spinner frames
			hasGlyph = true
		case r >= 0x2580 && r <= 0x259F: // block-element bar fills
			hasGlyph = true
		case r == 0x2E28 || r == 0x2E29: // ⸨ ⸩ npm progress brackets
			hasGlyph = true
		case r == ' ' || r == '\t':
			// whitespace filler is allowed between glyphs
		default:
			return false
		}
	}
	return hasGlyph
}

// Format compresses npm output; non-npm output falls back to generic.
func (n *NPM) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeNPM(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "npm: non-npm output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(n, raw, scrubbed, 0, "npm: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var deprecations []string // collected deprecation lines (Balanced+)
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if n.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isNPMDeprecation(t) {
			// Gather deprecation warnings; decide whether to collapse them
			// after we know how many there are.
			deprecations = append(deprecations, line)
			continue
		}
		if isNPMNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	// Collapse the deprecation warnings only when the summary is genuinely
	// smaller than the listing — otherwise keep the entries so the output
	// never grows past the lines it would replace (mirrors the git
	// formatter's untracked-file collapse).
	if len(deprecations) > 0 {
		listing := strings.Join(deprecations, "\n")
		summary := fmt.Sprintf("  (+%d deprecation warnings)", len(deprecations))
		if len(summary) < len(listing) {
			kept = append(kept, summary)
			dropped += len(deprecations)
		} else {
			kept = append(kept, deprecations...)
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("npm: %s, %d lines dropped", level, dropped)
	res := enforceCritical(n, raw, compact, dropped, notes)
	return res, true
}
