package formatter

import (
	"fmt"
	"strings"
)

// Make compresses the output of GNU `make` builds. make echoes each recipe
// command line before running it and interleaves the compiler's own output,
// so a large build drowns its diagnostics in recipe echoes and per-directory
// chatter. The signal an agent acts on — make's own failure markers
// ("*** …", "make: *** [target] Error 1") and the compiler diagnostics
// (": error:", ": fatal error:", link errors) — is always kept; the recipe
// echo (long "gcc …"/"cc …" invocation lines) and the "make[1]: Entering /
// Leaving directory" chatter are noise dropped at Balanced+. At Aggressive
// the advisory ": warning:" lines are dropped too, so a build that only
// failed on errors shrinks to the errors themselves.
//
// Output that does not resemble make is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Make struct{}

// NewMake returns the make formatter.
func NewMake() *Make { return &Make{} }

// Command reports the make command token.
func (m *Make) Command() string { return "make" }

// CriticalLine treats make's failure markers and compiler diagnostics as
// critical: make's own error lines ("make: *** [target] Error 1"), the
// "*** " fatal markers ("*** No rule to make target"), compiler error lines
// (": error:", ": fatal error:"), an "Error " followed by a status code,
// linker failures ("undefined reference", "ld: "). Advisory ": warning:"
// lines are never critical.
func (m *Make) CriticalLine(line string) bool {
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
	case strings.HasPrefix(t, "make:") && strings.Contains(t, "Error"),
		strings.Contains(t, "*** "),
		strings.Contains(t, ": error:"),
		strings.Contains(t, ": fatal error:"),
		isMakeErrorCode(t),
		strings.Contains(t, "undefined reference"),
		strings.HasPrefix(t, "ld: "):
		return true
	}
	return false
}

// isMakeErrorCode reports whether t carries an "Error <code>" status such as
// make prints when a recipe exits non-zero ("… Error 1", "… Error 2").
func isMakeErrorCode(t string) bool {
	_, rest, found := strings.Cut(t, "Error ")
	if !found {
		return false
	}
	return len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9'
}

// looksLikeMake reports whether b resembles make build output.
func looksLikeMake(b []byte) bool {
	s := string(b)
	for _, tok := range []string{
		"make[", "make:", "*** ", "gcc", "cc ", "g++", "clang", "Makefile", "make ",
	} {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}

// makeEchoPrefixes are the recipe-command tools make echoes verbatim before
// running them. A long line starting with one of these is a command echo,
// not a diagnostic, and is safe to drop at Balanced+ (a diagnostic line
// carrying ": error:" is classified critical and kept before this check).
var makeEchoPrefixes = []string{"gcc ", "cc ", "g++ ", "clang ", "ar ", "ranlib "}

// isMakeRecipeEcho reports the echoed compiler/archiver invocation lines. It
// requires a non-trivial length so a short, meaningful line is never mistaken
// for a command echo.
func isMakeRecipeEcho(t string) bool {
	if len(t) <= 12 {
		return false
	}
	for _, p := range makeEchoPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// Format compresses make output; non-make output falls back to generic.
func (m *Make) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeMake(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "make: non-make output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(m, raw, scrubbed, 0, "make: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if m.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		// Balanced+: drop the recipe echo and per-directory chatter.
		if strings.HasPrefix(t, "make[") || isMakeRecipeEcho(t) {
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
	notes := fmt.Sprintf("make: %s, %d lines dropped", level, dropped)
	res := enforceCritical(m, raw, compact, dropped, notes)
	return res, true
}
