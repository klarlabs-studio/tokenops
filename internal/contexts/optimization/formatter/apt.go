package formatter

import (
	"fmt"
	"strings"
)

// Apt compresses the output of `apt` / `apt-get install` / `apt-get update`
// and related commands. The change summary ("N upgraded, M newly installed,
// K to remove and L not upgraded."), any error ("E:" apt errors, "Err:" fetch
// failures), and the package-change headers ("The following NEW packages will
// be installed", "The following packages will be REMOVED") are the signal an
// agent acts on and are always kept.
//
// The per-repository fetch chatter ("Get:", "Hit:", "Ign:"), the index-read
// preamble ("Reading package lists…", "Building dependency tree…", "Reading
// state information…"), the "Fetched N kB" line, and the per-package unpack
// progress ("Preparing to unpack …", "Unpacking …", "Setting up …",
// "Processing triggers for …", "Progress: [ 50%]") carry no state and are
// stripped at Balanced and above. At Aggressive the advisory "W:" warning
// lines are additionally dropped and the indented NEW-packages listing body
// collapses to a single count — the collapse uses the same size guard as the
// git formatter's untracked-file block, so it never produces output larger
// than the lines it replaces.
//
// Output that does not resemble apt is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type Apt struct{}

// NewApt returns the apt formatter.
func NewApt() *Apt { return &Apt{} }

// Command reports the apt command token.
func (a *Apt) Command() string { return "apt" }

// Aliases registers the formatter under "apt-get" so apt-get output routes
// here too. Command() remains the canonical token.
func (a *Apt) Aliases() []string { return []string{"apt-get"} }

// CriticalLine treats error and change-summary signal as critical: apt error
// lines ("E:" prefix), fetch errors (embedded "Err:"), the install/upgrade
// change summary (any line carrying "not upgraded"), and the package-change
// headers so the agent sees what will be added or removed. Advisory "W:"
// warnings are never critical.
func (a *Apt) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "E:"),
		strings.Contains(t, "Err:"),
		strings.Contains(t, "not upgraded"),
		strings.HasPrefix(t, "The following packages will be REMOVED"),
		strings.HasPrefix(t, "The following NEW packages will be installed"):
		return true
	}
	return false
}

// looksLikeApt reports whether b resembles apt/apt-get output.
func looksLikeApt(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Reading package lists") ||
		strings.Contains(s, "Get:") ||
		strings.Contains(s, "Fetched ") ||
		strings.Contains(s, "Unpacking ") ||
		strings.Contains(s, "Setting up ") ||
		strings.Contains(s, "apt") ||
		strings.Contains(s, "Preparing to unpack") ||
		strings.Contains(s, "newly installed")
}

// isAptNoise reports the fetch/preamble/unpack progress lines that carry no
// state and are safe to drop at Balanced+. The change summary and the
// package-change headers are handled by CriticalLine and are never dropped.
func isAptNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Get:"),
		strings.HasPrefix(t, "Hit:"),
		strings.HasPrefix(t, "Ign:"),
		strings.HasPrefix(t, "Reading package lists"),
		strings.HasPrefix(t, "Building dependency tree"),
		strings.HasPrefix(t, "Reading state information"),
		strings.HasPrefix(t, "Fetched "),
		strings.HasPrefix(t, "Preparing to unpack"),
		strings.HasPrefix(t, "Unpacking "),
		strings.HasPrefix(t, "Setting up "),
		strings.HasPrefix(t, "Processing triggers for"),
		strings.HasPrefix(t, "Progress:"):
		return true
	}
	return false
}

// isAptNewHeader reports the NEW-packages header line, which opens the
// indented package-name listing collapsed at Aggressive.
func isAptNewHeader(t string) bool {
	return strings.HasPrefix(t, "The following NEW packages will be installed")
}

// countAptPackages counts the whitespace-separated package names across the
// collected NEW-packages listing body.
func countAptPackages(body []string) int {
	n := 0
	for _, line := range body {
		n += len(strings.Fields(line))
	}
	return n
}

// Format compresses apt output; non-apt output falls back to generic.
func (a *Apt) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeApt(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "apt: non-apt output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(a, raw, scrubbed, 0, "apt: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var newPkgBody []string // indented NEW-packages listing (Aggressive collapse)
	inNewList := false
	dropped := 0

	// flush collapses the collected NEW-packages listing body when the
	// summary is genuinely smaller than the listing — otherwise it keeps the
	// entries so the output never grows past the lines it replaces (mirrors
	// the git formatter's untracked-file collapse).
	flush := func() {
		if len(newPkgBody) == 0 {
			return
		}
		listing := strings.Join(newPkgBody, "\n")
		summary := fmt.Sprintf("  (+%d packages)", countAptPackages(newPkgBody))
		if len(summary) < len(listing) {
			kept = append(kept, summary)
			dropped += len(newPkgBody)
		} else {
			kept = append(kept, newPkgBody...)
		}
		newPkgBody = nil
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if a.CriticalLine(t) {
			// A critical line that is not the NEW header terminates any
			// listing body in progress (e.g. the change summary that follows).
			if inNewList && !isAptNewHeader(t) {
				flush()
				inNewList = false
			}
			kept = append(kept, line)
			// Only Aggressive collapses the listing body; at Balanced the
			// indented package names fall through and are kept in place.
			if isAptNewHeader(t) {
				inNewList = level == LossAggressive
			}
			continue
		}
		if inNewList {
			if strings.HasPrefix(line, " ") && t != "" {
				newPkgBody = append(newPkgBody, line)
				continue
			}
			flush()
			inNewList = false
		}
		if isAptNoise(t) || t == "" {
			dropped++
			continue
		}
		// Aggressive additionally sheds the advisory "W:" warning lines that
		// Balanced keeps.
		if level == LossAggressive && strings.HasPrefix(t, "W:") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	flush() // listing body that ran to end of output

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("apt: %s, %d lines dropped", level, dropped)
	res := enforceCritical(a, raw, compact, dropped, notes)
	return res, true
}
