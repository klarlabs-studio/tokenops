package formatter

import (
	"fmt"
	"strings"
)

// Helm compresses the output of `helm install` / `helm upgrade`. A successful
// release prints a small block of release metadata (NAME, LAST DEPLOYED,
// NAMESPACE, STATUS, REVISION) followed by a long "NOTES:" block — the chart's
// templated post-install instructions, frequently dozens of lines that carry
// no state an agent must act on. The release STATUS line, any table row
// reporting a failed/pending resource, and the terminal "Error: …" line are
// the signal an agent acts on and are always kept; the NOTES block is stripped
// at Balanced and above. At Aggressive the "LAST DEPLOYED"/"NAMESPACE" metadata
// is dropped as well, leaving NAME/STATUS/REVISION.
//
// Output that does not resemble Helm is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type Helm struct{}

// NewHelm returns the helm formatter.
func NewHelm() *Helm { return &Helm{} }

// Command reports the helm command token.
func (h *Helm) Command() string { return "helm" }

// CriticalLine treats release status and failure signal as critical: the
// terminal "Error: …" line, the release "STATUS: …" line, and any table row
// reporting a failed or pending resource. Release metadata (NAME/NAMESPACE/
// REVISION/LAST DEPLOYED) and the templated NOTES text are never critical.
func (h *Helm) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "Error"),
		strings.HasPrefix(t, "STATUS:"),
		strings.Contains(t, "failed"),
		strings.Contains(t, "pending"):
		return true
	}
	return false
}

// looksLikeHelm reports whether b resembles helm install/upgrade output.
func looksLikeHelm(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "STATUS:") ||
		strings.Contains(s, "LAST DEPLOYED") ||
		strings.Contains(s, "REVISION:") ||
		strings.Contains(s, "helm") ||
		strings.Contains(s, "NAME:") ||
		strings.Contains(s, "Release ")
}

// isHelmAggressiveNoise reports the release metadata dropped only at
// Aggressive: the "LAST DEPLOYED" timestamp and the "NAMESPACE:" line. The
// critical STATUS line and the NAME/REVISION identity survive.
func isHelmAggressiveNoise(t string) bool {
	return strings.HasPrefix(t, "LAST DEPLOYED") ||
		strings.HasPrefix(t, "NAMESPACE:")
}

// Format compresses helm output; non-helm output falls back to generic.
func (h *Helm) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeHelm(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "helm: non-helm output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(h, raw, scrubbed, 0, "helm: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	inNotes := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		// Critical lines survive regardless of the NOTES boundary.
		if h.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		// Once the NOTES: block starts, drop it through end of output.
		if inNotes {
			dropped++
			continue
		}
		if t == "NOTES:" {
			inNotes = true
			dropped++
			continue
		}
		if level == LossAggressive && isHelmAggressiveNoise(t) {
			dropped++
			continue
		}
		if t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("helm: %s, %d lines dropped", level, dropped)
	res := enforceCritical(h, raw, compact, dropped, notes)
	return res, true
}
