package formatter

import (
	"fmt"
	"strings"
)

// SBT compresses the output of `sbt` (Scala Build Tool) runs. sbt is noisy —
// a build emits a stream of "[info] …" lines for resolving, compiling, and
// download progress, plus "[warn] …" advisories. The failure and result
// signal an agent acts on — "[error] …" lines (including compiler
// "path.scala:10:5: type mismatch" errors), the "Compilation failed" marker,
// and the terminal "[success] Total time: …" line — is always kept; the
// "[info]" progress chatter is noise stripped at Balanced and above. At
// Aggressive the "[warn]" advisory lines are dropped as well.
//
// Output that does not resemble sbt is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type SBT struct{}

// NewSBT returns the sbt formatter.
func NewSBT() *SBT { return &SBT{} }

// Command reports the sbt command token.
func (s *SBT) Command() string { return "sbt" }

// CriticalLine treats failure and result signal as critical: any "[error]"
// line, any line reporting "Compilation failed", and the terminal
// "[success]" line (kept so the result survives). "[info]" and "[warn]"
// lines are never critical.
func (s *SBT) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "[error]"),
		strings.Contains(t, "Compilation failed"),
		strings.Contains(t, "[success]"):
		return true
	}
	return false
}

// looksLikeSBT reports whether b resembles sbt build output.
func looksLikeSBT(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "[info]") ||
		strings.Contains(s, "[success]") ||
		strings.Contains(s, "sbt") ||
		strings.Contains(s, "Total time") ||
		strings.Contains(s, "[error]")
}

// isSBTInfoNoise reports the "[info]" progress chatter (resolving, compiling,
// download progress) that carries no state and is safe to drop at Balanced+.
// The critical "[success]" line is recognised by CriticalLine before this
// check runs, so it is never eaten here.
func isSBTInfoNoise(t string) bool {
	return strings.HasPrefix(t, "[info]")
}

// Format compresses sbt output; non-sbt output falls back to generic.
func (s *SBT) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeSBT(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "sbt: non-sbt output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(s, raw, scrubbed, 0, "sbt: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if s.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isSBTInfoNoise(t) || t == "" {
			dropped++
			continue
		}
		if level == LossAggressive && strings.HasPrefix(t, "[warn]") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("sbt: %s, %d lines dropped", level, dropped)
	res := enforceCritical(s, raw, compact, dropped, notes)
	return res, true
}
