package formatter

import (
	"fmt"
	"strings"
)

// Cargo compresses the output of Rust's `cargo` (build, check, test). The
// compiler diagnostics an agent acts on — errors, the location arrows that
// follow them, test failures, and panics — are always kept; the transient
// progress chatter cargo prints ("   Compiling foo v0.1.0", download and
// build banners, the "    Finished" line) is noise dropped at Balanced+.
// At Aggressive the advisory "warning:" lines are dropped too, so a build
// that only failed on errors shrinks to the errors themselves.
//
// Output that does not resemble cargo is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Cargo struct{}

// NewCargo returns the cargo formatter.
func NewCargo() *Cargo { return &Cargo{} }

// Command reports the cargo command token.
func (c *Cargo) Command() string { return "cargo" }

// CriticalLine treats compiler errors and test failures as critical:
// error lines ("error", "error[E0308]: …"), the terminal "test result:
// FAILED" summary, the per-failure headers under a "failures:" block
// ("---- test_x stdout ----"), thread panics, and the "could not compile"
// bail-out. Advisory "warning:" lines are never critical.
func (c *Cargo) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "error"),
		strings.Contains(t, "error[E"),
		strings.Contains(t, "test result: FAILED"),
		strings.HasPrefix(t, "---- ") && strings.Contains(t, " stdout ----"),
		strings.HasPrefix(t, "thread '") && strings.Contains(t, "panicked"),
		strings.Contains(t, "could not compile"):
		return true
	}
	return false
}

// looksLikeCargo reports whether b resembles cargo output.
func looksLikeCargo(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Compiling ") ||
		strings.Contains(s, "cargo") ||
		strings.Contains(s, "error[E") ||
		strings.Contains(s, "Finished") ||
		strings.Contains(s, "test result:")
}

// isCargoProgress reports the transient progress banners cargo emits while
// resolving, downloading, and building crates. They carry no diagnostic
// signal and are safe to drop at Balanced+.
func isCargoProgress(t string) bool {
	switch {
	case strings.HasPrefix(t, "Compiling "),
		strings.HasPrefix(t, "Downloading"),
		strings.HasPrefix(t, "Downloaded"),
		strings.HasPrefix(t, "Blocking"),
		strings.HasPrefix(t, "Running "),
		strings.HasPrefix(t, "Finished"),
		strings.HasPrefix(t, "Building"):
		return true
	}
	return false
}

// isCargoWarning reports the advisory warning lines cargo/rustc print. They
// are informational, not blocking, so Aggressive drops them.
func isCargoWarning(t string) bool {
	return strings.HasPrefix(t, "warning:")
}

// Format compresses cargo output; non-cargo output falls back to generic.
func (c *Cargo) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeCargo(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "cargo: non-cargo output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(c, raw, scrubbed, 0, "cargo: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if c.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isCargoProgress(t) {
			dropped++
			continue
		}
		if level == LossAggressive && isCargoWarning(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("cargo: %s, %d lines dropped", level, dropped)
	res := enforceCritical(c, raw, compact, dropped, notes)
	return res, true
}
