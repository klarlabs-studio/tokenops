package formatter

import (
	"fmt"
	"strings"
)

// Bazel compresses the output of `bazel build` / `bazel test`. Bazel is
// famously noisy: it redraws "Loading:" / "Analyzing:" / "[N / M] building
// …" progress lines constantly, prints a wall of "INFO:" chatter, lists
// every up-to-date target with its produced artifact paths, and reports a
// "PASSED in …" line for every green test. The signal an agent acts on —
// "ERROR:" lines, "FAILED" lines (a failing "//pkg:test FAILED …" or the
// terminal "FAILED: Build did NOT complete successfully"), compiler errors
// ("path:line: error:"), and the "Executed N out of M tests" summary — is
// always kept.
//
// The progress frames, the "INFO:" chatter (except the terminal
// "Build completed" summary), the "Target //… up-to-date:" blocks together
// with their indented artifact paths, and the per-test "PASSED in …" lines
// are dropped at Balanced and above. At Aggressive the surviving
// non-critical target lines collapse to a single count. Every critical line
// survives at every level.
//
// This is a differentiator: RTK ships no Bazel formatter, so its Bazel
// output stays maximally noisy.
//
// Output that does not resemble Bazel is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Bazel struct{}

// NewBazel returns the bazel formatter.
func NewBazel() *Bazel { return &Bazel{} }

// Command reports the bazel command token.
func (b *Bazel) Command() string { return "bazel" }

// CriticalLine treats failure and result signal as critical: "ERROR:"
// lines, any "FAILED" line (a failing "//pkg:test  FAILED in …" line and the
// terminal "FAILED: Build did NOT complete successfully"), compiler errors
// ("… : error: …"), and the "Executed N out of M tests" summary. "PASSED"
// and "up-to-date" lines are never critical.
func (b *Bazel) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "ERROR:"),
		strings.Contains(t, "FAILED"),
		strings.Contains(t, ": error:"):
		return true
	case strings.HasPrefix(t, "Executed ") && strings.Contains(t, " out of "):
		return true
	}
	return false
}

// looksLikeBazel reports whether b resembles Bazel build/test output.
func looksLikeBazel(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Loading:") ||
		strings.Contains(s, "Analyzing:") ||
		strings.Contains(s, "INFO:") ||
		strings.Contains(s, "Target //") ||
		strings.Contains(s, "bazel") ||
		strings.Contains(s, "Executed ") ||
		strings.Contains(s, "bazel-bin")
}

// isBazelProgress reports the redrawn progress lines: "Loading:" /
// "Analyzing:" phase lines and the "[N / M] building …" execution frames.
func isBazelProgress(t string) bool {
	if strings.HasPrefix(t, "Loading:") || strings.HasPrefix(t, "Analyzing:") {
		return true
	}
	// "[12 / 34] building //pkg:test; 4s" execution frames.
	if strings.HasPrefix(t, "[") {
		if i := strings.Index(t, "]"); i > 0 && strings.Contains(t[1:i], " / ") {
			return true
		}
	}
	return false
}

// isBazelBuildSummary reports the terminal "INFO: Build completed …" line,
// which is kept even though it is an INFO line.
func isBazelBuildSummary(t string) bool {
	return strings.HasPrefix(t, "INFO:") && strings.Contains(t, "Build completed")
}

// isBazelInfoNoise reports the "INFO:" chatter that carries no state, which
// is every INFO line except the terminal build-completed summary.
func isBazelInfoNoise(t string) bool {
	return strings.HasPrefix(t, "INFO:") && !isBazelBuildSummary(t)
}

// isBazelTestPass reports the per-test success lines ("//pkg:test  PASSED
// in 0.3s"), safe to drop at Balanced+.
func isBazelTestPass(t string) bool {
	return strings.HasPrefix(t, "//") && strings.Contains(t, "PASSED")
}

// isBazelTargetHeader reports the "Target //… up-to-date:" header that opens
// a block of indented artifact-path lines.
func isBazelTargetHeader(t string) bool {
	return strings.HasPrefix(t, "Target //") && strings.HasSuffix(t, "up-to-date:")
}

// isBazelTargetLine reports the non-critical target/label lines that survive
// Balanced and collapse to a count at Aggressive.
func isBazelTargetLine(t string) bool {
	return strings.HasPrefix(t, "Target //") || strings.HasPrefix(t, "//")
}

// Format compresses bazel output; non-bazel output falls back to generic.
func (b *Bazel) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeBazel(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "bazel: non-bazel output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(b, raw, scrubbed, 0, "bazel: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var targets []string // surviving non-critical target lines (aggressive)
	inUpToDate := false
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)

		// "Target //… up-to-date:" opens a block: drop the header and the
		// indented artifact-path lines that follow it.
		if isBazelTargetHeader(t) {
			inUpToDate = true
			dropped++
			continue
		}
		if inUpToDate {
			if line != "" && (line[0] == ' ' || line[0] == '\t') {
				dropped++
				continue
			}
			inUpToDate = false
		}

		if b.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		// Balanced+: drop progress frames, INFO chatter (except the terminal
		// build-completed summary), and per-test PASSED lines.
		if t == "" || isBazelProgress(t) || isBazelInfoNoise(t) || isBazelTestPass(t) {
			dropped++
			continue
		}
		// Aggressive: collect the surviving non-critical target lines for a
		// single collapsed count.
		if level == LossAggressive && isBazelTargetLine(t) {
			targets = append(targets, line)
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	// Collapse the surviving targets only when the summary is genuinely
	// smaller, so Aggressive never grows past the Balanced rendering.
	if len(targets) > 0 {
		summary := fmt.Sprintf("(+%d build targets, run `bazel build` for the list)", len(targets))
		listing := strings.Join(targets, "\n")
		if len(summary) < len(listing) {
			kept = append(kept, summary)
		} else {
			kept = append(kept, targets...)
			dropped -= len(targets)
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("bazel: %s, %d lines dropped", level, dropped)
	res := enforceCritical(b, raw, compact, dropped, notes)
	return res, true
}
