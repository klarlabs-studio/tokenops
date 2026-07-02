package formatter

import (
	"fmt"
	"strings"
)

// CMake compresses the output of a CMake configure+build. The configure
// pass floods the log with progress probes ("-- The C compiler
// identification is …", "-- Detecting …", "-- Found …", "-- Check … -
// done") and the build pass prints a "[ NN%] Building/Linking …" line per
// object. The signal an agent acts on — CMake's own failure markers
// ("CMake Error at CMakeLists.txt:5"), the underlying make fatal markers
// ("*** …", "gmake[1]: *** [foo] Error 1"), and the compiler diagnostics
// (": error:", ": fatal error:") — is always kept; the "-- " configure
// probes and the "[ NN%] " build progress are noise dropped at Balanced+.
// At Aggressive the advisory ": warning:" lines are dropped too.
//
// Output that does not resemble CMake is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type CMake struct{}

// NewCMake returns the cmake formatter.
func NewCMake() *CMake { return &CMake{} }

// Command reports the cmake command token.
func (c *CMake) Command() string { return "cmake" }

// CriticalLine treats CMake's failure markers, the underlying make fatal
// markers, and compiler diagnostics as critical: compiler error lines
// (": error:", ": fatal error:"), CMake's own "CMake Error" lines, the
// "*** " make fatal markers, and an "Error " followed by a status code
// ("gmake[1]: *** [foo] Error 1"). Advisory ": warning:" lines and the
// "-- " configure/detection lines are never critical.
func (c *CMake) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// Advisory warnings are informational, not blocking. Check first so a
	// compiler warning line is never misclassified as an error.
	if strings.Contains(t, ": warning:") {
		return false
	}
	switch {
	case strings.Contains(t, ": error:"),
		strings.Contains(t, ": fatal error:"),
		strings.HasPrefix(t, "CMake Error"),
		strings.Contains(t, "*** "),
		isMakeErrorCode(t):
		return true
	}
	return false
}

// looksLikeCMake reports whether b resembles CMake configure/build output.
func looksLikeCMake(b []byte) bool {
	s := string(b)
	for _, tok := range []string{
		"-- ", "cmake", "CMake", "Building CXX", "Configuring done", "[ ",
	} {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}

// isCMakeConfigureLine reports the "-- " configure/detection lines CMake
// prints while probing the toolchain. They carry no failure signal and are
// safe to drop at Balanced+ (a diagnostic line carrying ": error:" is
// classified critical and kept before this check).
func isCMakeConfigureLine(t string) bool {
	return strings.HasPrefix(t, "-- ")
}

// isCMakeBuildProgress reports the "[ NN%] Building/Linking …" progress
// lines the build pass prints per object. They are per-action chatter,
// safe to drop at Balanced+.
func isCMakeBuildProgress(t string) bool {
	if !strings.HasPrefix(t, "[") {
		return false
	}
	head, tail, found := strings.Cut(t, "]")
	if !found {
		return false
	}
	if !strings.Contains(head, "%") {
		return false
	}
	rest := strings.TrimSpace(tail)
	return strings.HasPrefix(rest, "Building") || strings.HasPrefix(rest, "Linking")
}

// Format compresses cmake output; non-cmake output falls back to generic.
func (c *CMake) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeCMake(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "cmake: non-cmake output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(c, raw, scrubbed, 0, "cmake: conservative scrub")
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
		// Balanced+: drop the configure probes and build progress chatter.
		if isCMakeConfigureLine(t) || isCMakeBuildProgress(t) {
			dropped++
			continue
		}
		if level == LossAggressive && strings.Contains(t, ": warning:") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("cmake: %s, %d lines dropped", level, dropped)
	res := enforceCritical(c, raw, compact, dropped, notes)
	return res, true
}
