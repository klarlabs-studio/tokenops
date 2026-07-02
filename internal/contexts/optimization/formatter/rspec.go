package formatter

import (
	"fmt"
	"strings"
)

// RSpec compresses `rspec` output. The Failures section — its header, the
// numbered "  N) Thing does something" entries, the "Failure/Error:" detail
// lines, the "expected:"/"got:" assertion lines — and the terminal summary
// when it reports a failure are the signal an agent acts on and are always
// kept. The "Randomized with seed" footer, the "Finished in X seconds"
// timing, "Run options:" preamble, and the pure-pass progress-dots line are
// noise dropped at Balanced+.
//
// A progress line carrying an F (a failure marker) is never dropped: losing
// it would hide that a spec failed. Non-rspec output that reaches this
// formatter is handed to the generic noise scrub.
type RSpec struct{}

// NewRSpec returns the rspec formatter.
func NewRSpec() *RSpec { return &RSpec{} }

// Command reports the rspec command token.
func (r *RSpec) Command() string { return "rspec" }

// CriticalLine treats failure signal as critical: the "Failures:" section
// header, the numbered "  N) …" failure entries, "Failure/Error:" lines,
// the "expected"/"got" assertion detail lines, and the terminal summary
// when it reports a failure. Passing progress dots are never critical.
func (r *RSpec) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case t == "Failures:",
		isRSpecFailureEntry(t),
		strings.HasPrefix(t, "Failure/Error:"),
		strings.HasPrefix(t, "expected"),
		strings.HasPrefix(t, "got"),
		strings.Contains(t, "failure"):
		return true
	}
	return false
}

// isRSpecFailureEntry reports a numbered failure entry ("  1) Thing does
// something") from the Failures section.
func isRSpecFailureEntry(t string) bool {
	i := strings.Index(t, ") ")
	if i <= 0 {
		return false
	}
	for _, c := range t[:i] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// looksLikeRSpec reports whether b resembles rspec output.
func looksLikeRSpec(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "examples,") ||
		strings.Contains(s, "Failures:") ||
		strings.Contains(s, "Finished in") ||
		strings.Contains(s, "rspec") ||
		strings.Contains(s, "example,")
}

// isRSpecNoise reports the timing, seed, and run-options lines that carry no
// failure signal and are safe to drop at Balanced+.
func isRSpecNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Randomized with seed"),
		strings.HasPrefix(t, "Finished in "),
		strings.HasPrefix(t, "Run options:"):
		return true
	}
	return false
}

// isRSpecPassProgress reports a pure-pass progress-dots line ("....") with no
// failure marker. A line carrying an F returns false so failure-bearing
// progress is never dropped.
func isRSpecPassProgress(t string) bool {
	if t == "" {
		return false
	}
	for _, c := range t {
		if c != '.' {
			return false
		}
	}
	return true
}

// Format compresses rspec output; non-rspec output falls back to generic.
func (r *RSpec) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeRSpec(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "rspec: non-rspec output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(r, raw, scrubbed, 0, "rspec: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if r.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isRSpecNoise(t) || isRSpecPassProgress(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("rspec: %s, %d lines dropped", level, dropped)
	res := enforceCritical(r, raw, compact, dropped, notes)
	return res, true
}
