package formatter

import (
	"fmt"
	"strings"
)

// Kubectl compresses the output of `kubectl` commands. It models two
// shapes an agent commonly acts on:
//
//   - `kubectl get` tabular output (NAME READY STATUS RESTARTS AGE …). The
//     column header carries the meaning of every field and is always kept;
//     rows signalling a non-healthy state (CrashLoopBackOff, ImagePullBackOff,
//     Pending, Evicted, OOMKilled, …) are the signal an agent acts on and are
//     always kept. At Aggressive, consecutive healthy rows collapse into a
//     single count so a large green listing shrinks to its problem rows.
//   - `kubectl describe` output (Key: value blocks + an Events: section). The
//     Key: value structure and every Warning event or Error/Failed/Back-off
//     line survive; verbose Labels:/Annotations: blobs and Normal events are
//     noise dropped at Balanced and above.
//
// Output that does not resemble kubectl is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not model.
type Kubectl struct{}

// NewKubectl returns the kubectl formatter.
func NewKubectl() *Kubectl { return &Kubectl{} }

// Command reports the kubectl command token.
func (k *Kubectl) Command() string { return "kubectl" }

// kubectlBadStatus are the substrings that mark a row or event as
// non-healthy. Presence of any of these in a line makes it critical — this
// covers both get-table STATUS columns and describe Error/Failed/Back-off
// event text, keeping the predicate tight and shape-agnostic.
var kubectlBadStatus = []string{
	"CrashLoopBackOff",
	"ImagePullBackOff",
	"ErrImagePull",
	"OOMKilled",
	"ContainerCreating",
	"Terminating",
	"Evicted",
	"Pending",
	"NotReady",
	"Back-off",
	"Failed",
	"Error",
}

// CriticalLine treats non-healthy state and error signal as critical: the
// table column header (so the agent keeps column meaning), any row or event
// line containing a bad-status substring, and describe Warning events. A
// healthy Running/Completed/Ready row and a Normal event are never critical.
func (k *Kubectl) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// The get-table header line carries the meaning of every column.
	if strings.HasPrefix(t, "NAME ") {
		return true
	}
	// describe Events: a Warning event row ("Warning  BackOff  …").
	if strings.HasPrefix(t, "Warning") {
		return true
	}
	for _, s := range kubectlBadStatus {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

// looksLikeKubectl reports whether b resembles kubectl output — either a
// get-table header, a describe field block, an Events section, or the
// literal command token.
func looksLikeKubectl(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "NAME ") ||
		strings.Contains(s, "Namespace:") ||
		strings.Contains(s, "Events:") ||
		strings.Contains(s, "kubectl")
}

// isKubectlTable reports whether b is `kubectl get` tabular output, detected
// by a column-header line beginning with "NAME ".
func isKubectlTable(b []byte) bool {
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "NAME ") {
			return true
		}
	}
	return false
}

// isKubectlNoiseHeader reports the describe field headers whose values are
// verbose multi-line blobs carrying no state the agent acts on. The header
// line and its indented continuation lines are dropped at Balanced+.
func isKubectlNoiseHeader(t string) bool {
	return strings.HasPrefix(t, "Labels:") ||
		strings.HasPrefix(t, "Annotations:")
}

// isKubectlNormalEvent reports a describe "Normal" event row, dropped at
// Balanced+ (Warning events are critical and always kept).
func isKubectlNormalEvent(t string) bool {
	return strings.HasPrefix(t, "Normal ") || t == "Normal"
}

// startsWithSpace reports whether line is an indented continuation line.
func startsWithSpace(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// Format compresses kubectl output; non-kubectl output falls back to generic.
func (k *Kubectl) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeKubectl(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "kubectl: non-kubectl output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(k, raw, scrubbed, 0, "kubectl: conservative scrub")
		return res, true
	}
	if isKubectlTable(scrubbed) {
		return k.formatTable(raw, scrubbed, level)
	}
	return k.formatDescribe(raw, scrubbed, level)
}

// formatTable compresses `kubectl get` tabular output. The header and every
// critical row are always kept. At Aggressive, consecutive healthy rows
// collapse to a count when the summary is genuinely smaller than the listing
// (the git untracked-block size guard), so Aggressive never grows past
// Balanced.
func (k *Kubectl) formatTable(raw, scrubbed []byte, level LossLevel) (Result, bool) {
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var healthy []string // consecutive healthy rows pending collapse (Aggressive)
	dropped := 0

	flush := func() {
		if len(healthy) == 0 {
			return
		}
		if level == LossAggressive {
			listing := strings.Join(healthy, "\n")
			summary := fmt.Sprintf("  (+%d healthy rows)", len(healthy))
			if len(summary) < len(listing) {
				kept = append(kept, summary)
				dropped += len(healthy)
			} else {
				kept = append(kept, healthy...)
			}
		} else {
			kept = append(kept, healthy...)
		}
		healthy = healthy[:0]
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			dropped++
			continue
		}
		if k.CriticalLine(t) {
			flush()
			kept = append(kept, line)
			continue
		}
		// A healthy data row.
		if level == LossAggressive {
			healthy = append(healthy, line)
			continue
		}
		kept = append(kept, line)
	}
	flush()

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("kubectl get: %s, %d lines dropped", level, dropped)
	res := enforceCritical(k, raw, compact, dropped, notes)
	return res, true
}

// formatDescribe compresses `kubectl describe` output. The Key: value
// structure, the Events header, and every critical (Warning / Error / Failed /
// Back-off) line survive; the verbose Labels:/Annotations: blobs and Normal
// events are dropped at Balanced+.
func (k *Kubectl) formatDescribe(raw, scrubbed []byte, level LossLevel) (Result, bool) {
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	skipping := false // inside a Labels:/Annotations: multi-line blob

	for _, line := range lines {
		t := strings.TrimSpace(line)

		// Critical lines survive regardless of any skip state.
		if k.CriticalLine(t) {
			skipping = false
			kept = append(kept, line)
			continue
		}

		// While inside a noise blob, drop indented continuation lines; a
		// blank line or a new top-level key ends the blob.
		if skipping {
			if line == "" || !startsWithSpace(line) {
				skipping = false
				// fall through and handle this line normally
			} else {
				dropped++
				continue
			}
		}

		if isKubectlNoiseHeader(t) {
			skipping = true
			dropped++
			continue
		}
		if isKubectlNormalEvent(t) {
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
	notes := fmt.Sprintf("kubectl describe: %s, %d lines dropped", level, dropped)
	res := enforceCritical(k, raw, compact, dropped, notes)
	return res, true
}
