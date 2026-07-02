package formatter

import (
	"fmt"
	"strings"
)

// Mvn compresses the output of `mvn` (Apache Maven) builds. Maven is
// extremely noisy — a single build emits hundreds of "[INFO] …" lines for
// lifecycle phases, plugin banners, and artifact resolution. The failure
// and result signal an agent acts on — "[ERROR]" lines, the terminal
// "BUILD SUCCESS"/"BUILD FAILURE" line, and the "Tests run: …" surefire
// summary — is always kept; the "[INFO]" lifecycle chatter and the
// "Downloading from"/"Downloaded from"/"Progress" artifact-resolution lines
// are noise stripped at Balanced and above. At Aggressive the "[WARNING]"
// lines are dropped as well.
//
// Output that does not resemble Maven is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Mvn struct{}

// NewMvn returns the mvn formatter.
func NewMvn() *Mvn { return &Mvn{} }

// Command reports the mvn command token.
func (m *Mvn) Command() string { return "mvn" }

// CriticalLine treats failure and result signal as critical: any "[ERROR]"
// line, the terminal "BUILD FAILURE"/"BUILD SUCCESS" result line, the
// surefire "Tests run: …" summary, and any line reporting a failure
// ("FAIL"). "[WARNING]" lines are never critical.
func (m *Mvn) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "[ERROR]"),
		strings.Contains(t, "BUILD FAILURE"),
		strings.Contains(t, "BUILD SUCCESS"),
		strings.Contains(t, "Tests run:"),
		strings.Contains(t, "FAIL"):
		return true
	}
	return false
}

// looksLikeMvn reports whether b resembles Maven build output.
func looksLikeMvn(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "[INFO]") ||
		strings.Contains(s, "maven") ||
		strings.Contains(s, "BUILD SUCCESS") ||
		strings.Contains(s, "BUILD FAILURE") ||
		strings.Contains(s, "mvn") ||
		strings.Contains(s, "Apache Maven")
}

// isMvnInfoNoise reports the "[INFO]" lifecycle chatter that carries no
// state and is safe to drop at Balanced+. Critical "[INFO]" lines (the
// BUILD result and the "Tests run:" summary) are recognised by CriticalLine
// before this check runs, so they are never eaten here.
func isMvnInfoNoise(t string) bool {
	return strings.HasPrefix(t, "[INFO]")
}

// isMvnDownloadNoise reports the artifact-resolution progress lines Maven
// prints while fetching dependencies, dropped at Balanced+.
func isMvnDownloadNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Downloading from"),
		strings.HasPrefix(t, "Downloaded from"),
		strings.HasPrefix(t, "Progress"):
		return true
	}
	return false
}

// Format compresses mvn output; non-mvn output falls back to generic.
func (m *Mvn) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeMvn(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "mvn: non-mvn output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(m, raw, scrubbed, 0, "mvn: conservative scrub")
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
		if isMvnInfoNoise(t) || isMvnDownloadNoise(t) || t == "" {
			dropped++
			continue
		}
		if level == LossAggressive && strings.HasPrefix(t, "[WARNING]") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("mvn: %s, %d lines dropped", level, dropped)
	res := enforceCritical(m, raw, compact, dropped, notes)
	return res, true
}
