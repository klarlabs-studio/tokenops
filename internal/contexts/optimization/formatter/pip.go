package formatter

import (
	"fmt"
	"strings"
)

// Pip compresses the output of `pip install` (and `pip3 install`). The
// install summary ("Successfully installed <pkgs>"), any error, the
// dependency-resolver conflict report, and "Could not find a version"
// resolution failures are the signal an agent acts on and are always kept.
//
// The per-package resolution chatter ("Collecting foo", "Downloading …",
// "  Using cached …", "Requirement already satisfied: …") and the build
// progress lines ("Preparing metadata", "Building wheel", "Created wheel")
// carry no state and are stripped at Balanced and above. At Aggressive the
// advisory "WARNING:" lines are additionally dropped.
//
// Output that does not resemble pip is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type Pip struct{}

// NewPip returns the pip formatter.
func NewPip() *Pip { return &Pip{} }

// Command reports the pip command token.
func (p *Pip) Command() string { return "pip" }

// Aliases registers the formatter under "pip3" so pip3 output routes here
// too. Command() remains the canonical token.
func (p *Pip) Aliases() []string { return []string{"pip3"} }

// CriticalLine treats error and install-summary signal as critical: any
// error line ("ERROR"/"ERROR:" prefix or an embedded "error:"), the
// dependency-resolver conflict report, "Could not find a version" resolution
// failures, and the "Successfully installed" / "Successfully uninstalled"
// summaries. The advisory "WARNING: You are using pip version" notice is not
// critical.
func (p *Pip) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// The pip-version upgrade advisory is noise, not signal, even though it
	// begins with WARNING.
	if strings.HasPrefix(t, "WARNING: You are using pip version") {
		return false
	}
	switch {
	case strings.HasPrefix(t, "ERROR:"),
		strings.HasPrefix(t, "ERROR"),
		strings.Contains(t, "error:"),
		strings.Contains(t, "Could not find a version"),
		strings.HasPrefix(t, "Successfully installed"),
		strings.HasPrefix(t, "Successfully uninstalled"):
		return true
	}
	return false
}

// looksLikePip reports whether b resembles pip install output.
func looksLikePip(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Collecting ") ||
		strings.Contains(s, "Requirement already satisfied") ||
		strings.Contains(s, "Successfully installed") ||
		strings.Contains(s, "pip") ||
		strings.Contains(s, "Downloading ")
}

// isPipNoise reports the resolution/progress lines that carry no state and
// are safe to drop at Balanced+: collecting/downloading/cached chatter,
// already-satisfied requirements, and metadata/wheel build progress. The
// "Installing collected packages" line is a summary and is deliberately not
// included here so it survives.
func isPipNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Collecting "),
		strings.HasPrefix(t, "Downloading "),
		strings.HasPrefix(t, "Using cached "),
		strings.HasPrefix(t, "Requirement already satisfied:"),
		strings.HasPrefix(t, "Preparing metadata"),
		strings.HasPrefix(t, "Building wheel"),
		strings.HasPrefix(t, "Created wheel"):
		return true
	}
	return false
}

// Format compresses pip output; non-pip output falls back to generic.
func (p *Pip) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikePip(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "pip: non-pip output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(p, raw, scrubbed, 0, "pip: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if p.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isPipNoise(t) || t == "" {
			dropped++
			continue
		}
		// Aggressive additionally sheds the advisory WARNING lines that
		// Balanced keeps (the pip-version notice and similar).
		if level == LossAggressive && strings.HasPrefix(t, "WARNING:") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("pip: %s, %d lines dropped", level, dropped)
	res := enforceCritical(p, raw, compact, dropped, notes)
	return res, true
}
