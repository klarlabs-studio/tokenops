package formatter

import (
	"fmt"
	"strings"
)

// Gem compresses the output of `gem install` and related RubyGems commands.
// The install summary ("Successfully installed foo-1.2.3", "N gems
// installed") and any error ("ERROR:  Could not find a valid gem 'foo'", or a
// build failure) are the signal an agent acts on and are always kept.
//
// The per-gem fetch/build chatter ("Fetching foo-1.2.3.gem", "Building native
// extensions. This could take a while...") and the documentation-generation
// progress ("Parsing documentation for foo-1.2.3", "Installing ri
// documentation for foo-1.2.3", "Done installing documentation for foo after
// 2 seconds") carry no state beyond the install summary and are stripped at
// Balanced and above.
//
// Output that does not resemble gem is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type Gem struct{}

// NewGem returns the gem formatter.
func NewGem() *Gem { return &Gem{} }

// Command reports the gem command token.
func (g *Gem) Command() string { return "gem" }

// CriticalLine treats error and install-summary signal as critical: any line
// beginning "ERROR" or carrying an embedded "error"/"could not"/"failed"
// (case-insensitive), the "Successfully installed" summary, and the "N gems
// installed" tally. The fetch/build and documentation-generation chatter is
// never critical (note "Building native extensions. This could take a
// while..." contains "could" but not "could not", so it stays noise).
func (g *Gem) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(t, "ERROR"),
		strings.Contains(lower, "error"),
		strings.Contains(lower, "could not"),
		strings.Contains(lower, "failed"),
		strings.HasPrefix(t, "Successfully installed"),
		strings.Contains(t, "gems installed"):
		return true
	}
	return false
}

// looksLikeGem reports whether b resembles gem install output.
func looksLikeGem(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "gem ") ||
		strings.Contains(s, "Fetching ") ||
		strings.Contains(s, "Successfully installed") ||
		strings.Contains(s, "gems installed") ||
		strings.Contains(s, "Parsing documentation")
}

// isGemNoise reports the fetch/build and documentation-generation lines that
// carry no state beyond the install summary and are safe to drop at
// Balanced+. The "Successfully installed" / "N gems installed" summaries are
// deliberately not included here so they survive.
func isGemNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Fetching "),
		strings.HasPrefix(t, "Building native extensions"),
		strings.HasPrefix(t, "Parsing documentation"),
		strings.HasPrefix(t, "Installing ri documentation"),
		strings.HasPrefix(t, "Installing rdoc"),
		strings.HasPrefix(t, "Done installing documentation"):
		return true
	}
	return false
}

// Format compresses gem output; non-gem output falls back to generic.
func (g *Gem) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeGem(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "gem: non-gem output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(g, raw, scrubbed, 0, "gem: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if g.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isGemNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("gem: %s, %d lines dropped", level, dropped)
	res := enforceCritical(g, raw, compact, dropped, notes)
	return res, true
}
