package formatter

import (
	"fmt"
	"strings"
)

// Git compresses the output of `git status` (long/default form). The
// changed-file lines are the state an agent acts on and are always kept;
// the surrounding guidance ("use git restore…", "nothing added to
// commit…", branch-tracking chatter) is noise stripped at Balanced and
// above. At Aggressive, long untracked-file blocks collapse to a count.
//
// Only status output is transformed. Any other git subcommand output that
// reaches this formatter is handed to the generic noise scrub, so the
// formatter is never destructive on diffs, logs, or blames it does not
// model.
type Git struct{}

// NewGit returns the git formatter.
func NewGit() *Git { return &Git{} }

// Command reports the git command token.
func (g *Git) Command() string { return "git" }

// gitHintPrefixes are the human-guidance lines git prints under each status
// section. They carry no state and are safe to drop at Balanced+.
var gitHintPrefixes = []string{
	`(use "git`,
	`(commit or discard`,
	`(and have`,
}

// CriticalLine treats every worktree-state line as critical: staged,
// unstaged, and untracked entries. These are indented status lines and the
// "Untracked files:" style headers whose loss would blind the agent.
func (g *Git) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// Section headers that name state the agent must see.
	switch {
	case strings.HasPrefix(t, "Changes to be committed"),
		strings.HasPrefix(t, "Changes not staged"),
		strings.HasPrefix(t, "Untracked files"),
		strings.HasPrefix(t, "Unmerged paths"),
		strings.HasPrefix(t, "both modified"),
		strings.HasPrefix(t, "modified:"),
		strings.HasPrefix(t, "new file:"),
		strings.HasPrefix(t, "deleted:"),
		strings.HasPrefix(t, "renamed:"),
		strings.HasPrefix(t, "copied:"),
		strings.HasPrefix(t, "typechange:"):
		return true
	}
	return false
}

// Format compresses git output. Non-status output falls back to the
// generic scrub.
func (g *Git) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeStatus(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "git: non-status output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		// Conservative: only the generic scrub already applied.
		res := enforceCritical(g, raw, scrubbed, 0, "git status: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var untracked []string // collected untracked entry lines (aggressive)
	inUntracked := false
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)

		if strings.HasPrefix(t, "Untracked files") {
			inUntracked = true
			kept = append(kept, line)
			continue
		}
		if inUntracked {
			// A blank line or a new section header ends the block.
			switch {
			case t == "" || strings.HasSuffix(t, ":") && !strings.Contains(t, "/"):
				inUntracked = false
			case isGitHint(t):
				// The parenthetical guidance under the header is noise at
				// Balanced+ regardless; drop it before collecting entries.
				dropped++
				continue
			case level == LossAggressive:
				// Aggressive: gather untracked entries; decide whether to
				// collapse after we know how many there are.
				untracked = append(untracked, line)
				continue
			}
		}

		if g.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isGitHint(t) || isBranchChatter(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	// Collapse the untracked block only when the summary is genuinely
	// smaller than the listing — otherwise keep the entries so Aggressive
	// never produces larger output than Balanced.
	if len(untracked) > 0 {
		listing := strings.Join(untracked, "\n")
		summary := fmt.Sprintf("  (+%d untracked files, run `git status` for the list)", len(untracked))
		if len(summary) < len(listing) {
			kept = append(kept, summary)
			dropped += len(untracked)
		} else {
			kept = append(kept, untracked...)
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("git status: %s, %d lines dropped", level, dropped)
	res := enforceCritical(g, raw, compact, dropped, notes)
	return res, true
}

// looksLikeStatus reports whether b resembles `git status` long output.
func looksLikeStatus(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Changes ") ||
		strings.Contains(s, "Untracked files") ||
		strings.Contains(s, "nothing to commit") ||
		strings.Contains(s, "Unmerged paths")
}

// isGitHint reports the guidance lines git prints in parentheses.
func isGitHint(t string) bool {
	for _, p := range gitHintPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// isBranchChatter reports branch/tracking status lines that carry no
// worktree state the agent acts on.
func isBranchChatter(t string) bool {
	switch {
	case strings.HasPrefix(t, "On branch"),
		strings.HasPrefix(t, "Your branch is"),
		strings.HasPrefix(t, "nothing to commit"),
		strings.HasPrefix(t, "no changes added to commit"),
		strings.HasPrefix(t, "nothing added to commit"):
		return true
	}
	return false
}

// trimBlanks removes leading/trailing blank lines from a line slice.
func trimBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
