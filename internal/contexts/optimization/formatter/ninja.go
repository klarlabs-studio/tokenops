package formatter

import (
	"fmt"
	"strings"
)

// Ninja compresses the output of a `ninja` build. Ninja prints one
// "[N/M] Compiling/Linking …" progress line per action, so a large build
// drowns its diagnostics in per-action chatter. The signal an agent acts
// on — ninja's own failure markers ("FAILED: obj/foo.o", "ninja: build
// stopped: subcommand failed.", "ninja: error: …") and the compiler
// diagnostics (": error:", ": fatal error:") — is always kept; the
// "[N/M] " progress lines are noise dropped at Balanced+. At Aggressive
// the advisory ": warning:" lines are dropped too.
//
// Output that does not resemble ninja is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not
// model.
type Ninja struct{}

// NewNinja returns the ninja formatter.
func NewNinja() *Ninja { return &Ninja{} }

// Command reports the ninja command token.
func (n *Ninja) Command() string { return "ninja" }

// CriticalLine treats ninja's failure markers and compiler diagnostics as
// critical: compiler error lines (": error:", ": fatal error:"), ninja's
// "FAILED:" recipe-failure lines, and its "ninja: build stopped" /
// "ninja: error:" fatal lines. Advisory ": warning:" lines and the
// "[N/M] " progress lines are never critical.
func (n *Ninja) CriticalLine(line string) bool {
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
		strings.HasPrefix(t, "FAILED:"),
		strings.HasPrefix(t, "ninja: build stopped"),
		strings.HasPrefix(t, "ninja: error:"):
		return true
	}
	return false
}

// looksLikeNinja reports whether b resembles ninja build output.
func looksLikeNinja(b []byte) bool {
	s := string(b)
	for _, tok := range []string{
		"ninja", "[", "Compiling ", "Linking ", "build stopped",
	} {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}

// isNinjaProgress reports the "[N/M] …" progress lines ninja prints per
// action. They are per-action chatter, safe to drop at Balanced+ (a
// progress line that also carries ": error:" is classified critical and
// kept before this check).
func isNinjaProgress(t string) bool {
	if !strings.HasPrefix(t, "[") {
		return false
	}
	close := strings.Index(t, "]")
	if close < 1 {
		return false
	}
	inner := t[1:close]
	// Require "N/M" shape: a slash flanked by digits.
	slash := strings.Index(inner, "/")
	if slash <= 0 || slash >= len(inner)-1 {
		return false
	}
	return inner[slash-1] >= '0' && inner[slash-1] <= '9' &&
		inner[slash+1] >= '0' && inner[slash+1] <= '9'
}

// Format compresses ninja output; non-ninja output falls back to generic.
func (n *Ninja) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeNinja(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "ninja: non-ninja output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(n, raw, scrubbed, 0, "ninja: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if n.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		// Balanced+: drop the per-action progress lines.
		if isNinjaProgress(t) {
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
	notes := fmt.Sprintf("ninja: %s, %d lines dropped", level, dropped)
	res := enforceCritical(n, raw, compact, dropped, notes)
	return res, true
}
