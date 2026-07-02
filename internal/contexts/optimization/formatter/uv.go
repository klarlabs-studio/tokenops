package formatter

import (
	"fmt"
	"strings"
)

// UV compresses the output of `uv pip install` / `uv sync` and related
// commands. The resolution summary ("Resolved N packages in …"), the install
// summary ("Installed N packages in …"), any error ("error: …"), and
// "No solution found" resolution failures are the signal an agent acts on and
// are always kept.
//
// The per-package progress chatter ("Downloaded foo", "Prepared N packages")
// and the per-package install listing ("+ foo==1.2.3") carry no state beyond
// the summaries and are stripped at Balanced and above.
//
// Output that does not resemble uv is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type UV struct{}

// NewUV returns the uv formatter.
func NewUV() *UV { return &UV{} }

// Command reports the uv command token.
func (u *UV) Command() string { return "uv" }

// CriticalLine treats error and summary signal as critical: any error line
// (case-insensitive "error"), the "Installed N packages" and "Resolved N
// packages" summaries, and "No solution found" / "failed" resolution
// failures. The per-package "+ pkg==" listing and "Downloaded"/"Prepared"
// progress lines are never critical.
func (u *UV) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	switch {
	case strings.Contains(lower, "error"),
		strings.HasPrefix(t, "Installed "),
		strings.HasPrefix(t, "Resolved "),
		strings.Contains(t, "No solution found"),
		strings.Contains(lower, "failed"):
		return true
	}
	return false
}

// looksLikeUV reports whether b resembles uv output.
func looksLikeUV(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Resolved ") ||
		strings.Contains(s, "Installed ") ||
		strings.Contains(s, "uv ") ||
		strings.Contains(s, "Prepared ") ||
		strings.Contains(s, "Audited ")
}

// isUVNoise reports the progress/listing lines that carry no state beyond the
// summaries and are safe to drop at Balanced+: the "Downloaded"/"Prepared"
// progress chatter and the per-package "+ pkg==" install listing.
func isUVNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Downloaded "),
		strings.HasPrefix(t, "Prepared "),
		strings.HasPrefix(t, "+ "):
		return true
	}
	return false
}

// Format compresses uv output; non-uv output falls back to generic.
func (u *UV) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeUV(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "uv: non-uv output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(u, raw, scrubbed, 0, "uv: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if u.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isUVNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("uv: %s, %d lines dropped", level, dropped)
	res := enforceCritical(u, raw, compact, dropped, notes)
	return res, true
}
