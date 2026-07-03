package formatter

import (
	"fmt"
	"strings"
)

// Nomad compresses the output of HashiCorp Nomad status commands. It models
// two shapes an agent commonly acts on:
//
//   - `nomad job status` summary + allocation tables. The leading
//     "Key = value" summary block (ID, Name, Status, …) carries the job's
//     identity and current state; the "Allocations" table header carries the
//     meaning of every column and is always kept; allocation rows in a
//     non-healthy state (failed, lost, dead, pending, or any error line) are
//     the signal an agent acts on and are always kept. At Aggressive,
//     consecutive healthy running/complete allocation rows collapse into a
//     single count so a large green listing shrinks to its problem rows.
//   - `nomad node status` / `nomad alloc status` output, whose verbose
//     "Recent Events:" and "Placement Metrics" sub-blocks are noise dropped
//     at Balanced and above while the summary and tables survive.
//
// Output that does not resemble Nomad is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Nomad struct{}

// NewNomad returns the nomad formatter.
func NewNomad() *Nomad { return &Nomad{} }

// Command reports the nomad command token.
func (n *Nomad) Command() string { return "nomad" }

// nomadBadStatus are the substrings that mark an allocation row, summary
// line, or event as non-healthy. Presence of any (case-insensitive) makes a
// line critical — this covers both allocation STATUS columns (failed, lost,
// pending, dead) and "Error querying …" diagnostics, keeping the predicate
// tight and shape-agnostic.
var nomadBadStatus = []string{"error", "failed", "lost", "dead", "pending"}

// CriticalLine treats non-healthy state and error signal as critical: any
// line containing a bad-status substring, the job "Status = …" summary line
// (the single most load-bearing field), and the allocation table header (so
// the agent keeps column meaning). A healthy running/complete allocation row
// is never force-critical.
func (n *Nomad) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// The job "Status = running" summary line always survives.
	if isNomadStatusLine(t) {
		return true
	}
	// The allocation table header ("ID  Node ID  Task Group  …") carries the
	// meaning of every column.
	if strings.HasPrefix(t, "ID ") {
		return true
	}
	low := strings.ToLower(t)
	for _, s := range nomadBadStatus {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// isNomadStatusLine reports the job "Status = <state>" summary line,
// tolerating the column padding Nomad inserts ("Status        = running")
// while never matching a longer key like "StatusDescription = …".
func isNomadStatusLine(t string) bool {
	if !strings.HasPrefix(t, "Status") {
		return false
	}
	rest := strings.TrimSpace(t[len("Status"):])
	return strings.HasPrefix(rest, "=")
}

// looksLikeNomad reports whether b resembles Nomad status output — a summary
// block, an allocation table, or the literal command token.
func looksLikeNomad(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "nomad") ||
		strings.Contains(s, "Allocations") ||
		strings.Contains(s, "Task Group") ||
		strings.Contains(s, "Node ID") ||
		strings.Contains(s, "= running") ||
		strings.Contains(s, "Status =")
}

// isNomadVerboseHeader reports the sub-block headers whose bodies are verbose
// event/metric listings carrying no state the agent acts on. The header and
// its body (up to the next blank line) are dropped at Balanced+.
func isNomadVerboseHeader(t string) bool {
	return strings.HasPrefix(t, "Recent Events:") ||
		strings.HasPrefix(t, "Placement Metrics")
}

// isNomadHealthyAllocRow reports a healthy (running/complete) allocation row
// eligible for Aggressive collapse. It excludes "Key = value" summary lines
// so the leading summary block is never mistaken for an allocation row.
func isNomadHealthyAllocRow(t string) bool {
	if strings.Contains(t, " = ") {
		return false
	}
	low := strings.ToLower(t)
	return strings.Contains(low, "running") || strings.Contains(low, "complete")
}

// Format compresses nomad output; non-nomad output falls back to generic.
func (n *Nomad) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeNomad(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "nomad: non-nomad output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(n, raw, scrubbed, 0, "nomad: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var healthy []string // consecutive healthy alloc rows pending collapse (Aggressive)
	dropped := 0
	skipping := false // inside a Recent Events:/Placement Metrics sub-block

	flush := func() {
		if len(healthy) == 0 {
			return
		}
		if level == LossAggressive {
			listing := strings.Join(healthy, "\n")
			summary := fmt.Sprintf("  (+%d healthy allocations)", len(healthy))
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

		// Critical lines survive regardless of any skip state.
		if n.CriticalLine(t) {
			skipping = false
			flush()
			kept = append(kept, line)
			continue
		}

		// While inside a verbose sub-block, drop every line until the blank
		// line that terminates it.
		if skipping {
			if t == "" {
				skipping = false
			}
			dropped++
			continue
		}

		if isNomadVerboseHeader(t) {
			skipping = true
			dropped++
			continue
		}
		if t == "" {
			dropped++
			continue
		}

		// Aggressive: gather healthy allocation rows for collapse; other
		// content (summary lines, section headers) is kept verbatim.
		if level == LossAggressive && isNomadHealthyAllocRow(t) {
			healthy = append(healthy, line)
			continue
		}
		flush()
		kept = append(kept, line)
	}
	flush()

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("nomad: %s, %d lines dropped", level, dropped)
	res := enforceCritical(n, raw, compact, dropped, notes)
	return res, true
}
