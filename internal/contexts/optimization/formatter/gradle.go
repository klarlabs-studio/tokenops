package formatter

import (
	"fmt"
	"strings"
)

// Gradle compresses the output of `gradle` / `./gradlew` builds. The
// failure and result signal an agent acts on — the terminal
// "BUILD FAILED" / "BUILD SUCCESSFUL" line, "FAILURE:" blocks, the
// "* What went wrong:" section, "Execution failed for task …" lines,
// FAILED task lines ("> Task :app:compileJava FAILED"), and compiler
// errors (javac "path:line: error:" and Kotlin "e: …") — is always kept.
//
// The executed-ok task lines ("> Task :app:test", "> Task … UP-TO-DATE /
// SKIPPED / NO-SOURCE / FROM-CACHE"), daemon/download chatter, progress-bar
// frames, and the "Deprecated Gradle features were used" advisory are noise
// stripped at Balanced and above. FAILED tasks and every critical line
// survive at every level.
//
// Output that does not resemble Gradle is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not
// model.
type Gradle struct{}

// NewGradle returns the gradle formatter.
func NewGradle() *Gradle { return &Gradle{} }

// Command reports the gradle command token.
func (g *Gradle) Command() string { return "gradle" }

// CriticalLine treats failure and result signal as critical: the terminal
// build-status lines, "FAILURE:" blocks, FAILED task lines, compiler
// errors (javac ": error:" and Kotlin "e: "), the "What went wrong:"
// section headers, and "Execution failed for task" lines. Executed-ok and
// UP-TO-DATE/SKIPPED/NO-SOURCE task lines are never critical.
func (g *Gradle) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "BUILD FAILED"),
		strings.HasPrefix(t, "BUILD SUCCESSFUL"),
		strings.HasPrefix(t, "FAILURE:"),
		strings.HasPrefix(t, "* What went wrong:"),
		strings.HasPrefix(t, "What went wrong:"),
		strings.HasPrefix(t, "e: "),
		strings.Contains(t, "Execution failed for task"),
		strings.Contains(t, ": error:"):
		return true
	case strings.HasPrefix(t, "> Task ") && strings.HasSuffix(t, "FAILED"):
		return true
	}
	return false
}

// looksLikeGradle reports whether b resembles Gradle build output.
func looksLikeGradle(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "> Task ") ||
		strings.Contains(s, "BUILD SUCCESSFUL") ||
		strings.Contains(s, "BUILD FAILED") ||
		strings.Contains(s, "gradle") ||
		strings.Contains(s, "Gradle") ||
		strings.Contains(s, "> Configure")
}

// isGradleOkTask reports the non-failed "> Task :" lines — those ending
// UP-TO-DATE, SKIPPED, NO-SOURCE, FROM-CACHE, or with no suffix at all
// (executed-ok). FAILED task lines are handled by CriticalLine and never
// reach this check. These carry no failure signal and are safe to drop at
// Balanced+.
func isGradleOkTask(t string) bool {
	return strings.HasPrefix(t, "> Task ") && !strings.HasSuffix(t, "FAILED")
}

// isGradleNoise reports the daemon/download/progress chatter and the
// deprecation advisory that carry no state and are safe to drop at
// Balanced+.
func isGradleNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Download https://"),
		strings.HasPrefix(t, "Starting a Gradle Daemon"),
		strings.HasPrefix(t, "Welcome to Gradle"),
		strings.HasPrefix(t, "Deprecated Gradle features were used"):
		return true
	}
	return isGradleProgressLine(t)
}

// isGradleProgressLine reports the progress-bar frames Gradle draws while a
// build runs (e.g. "<=====> 75%"). Requiring the whole line to be composed
// of the bar glyphs, whitespace, digits, and '%' keeps the check from ever
// eating a content line.
func isGradleProgressLine(t string) bool {
	if t == "" || !strings.Contains(t, "%") {
		return false
	}
	for _, r := range t {
		switch {
		case r == '<' || r == '>' || r == '=' || r == '-' || r == '%':
		case r >= '0' && r <= '9':
		case r == ' ' || r == '\t':
		default:
			return false
		}
	}
	return true
}

// Format compresses gradle output; non-gradle output falls back to generic.
func (g *Gradle) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeGradle(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "gradle: non-gradle output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(g, raw, scrubbed, 0, "gradle: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if g.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		// Balanced+: drop executed-ok task lines, daemon/download/progress
		// chatter, and the deprecation advisory. Aggressive matches
		// Balanced here — the same noise is already gone, so output never
		// grows past the Balanced rendering.
		if isGradleOkTask(t) || isGradleNoise(t) || t == "" {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("gradle: %s, %d lines dropped", level, dropped)
	res := enforceCritical(g, raw, compact, dropped, notes)
	return res, true
}
