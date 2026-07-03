package formatter

import (
	"fmt"
	"strings"
)

// Packer compresses the output of `packer build`, a noisy image-build stream.
// The result and failure signal an agent acts on — the terminal
// "Build '<builder>' finished after …" / "errored after …" lines, the
// "==> Builds finished. The artifacts of successful builds are:" banner and
// the artifact lines beneath it, the "==> Some builds didn't complete
// successfully" line, and any line carrying an error — is always kept.
//
// The per-step progress ("==> amazon-ebs: Creating temporary keypair…") and
// the indented builder output echoes ("    amazon-ebs: <tool output>") are
// noise stripped at Balanced and above. At Aggressive the remaining "==> "
// progress collapses into a single count. Critical lines survive at every
// level.
//
// Output that does not resemble Packer is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Packer struct{}

// NewPacker returns the packer formatter.
func NewPacker() *Packer { return &Packer{} }

// Command reports the packer command token.
func (p *Packer) Command() string { return "packer" }

// packerCritical are the substrings that mark a line as build-result or
// failure signal. Presence of any (case-insensitive) makes the line critical.
var packerCritical = []string{
	"error",
	"errored",
	"failed",
	"didn't complete",
	"finished after",
	"artifacts of successful",
}

// CriticalLine treats build-result and failure signal as critical: any line
// carrying an error, the "Build '<builder>' finished after …" and
// "errored after …" lines, the "==> Builds finished" banner, and the
// "artifacts of successful builds" listing. Per-step "==> <builder>: <step>"
// progress and indented "    <builder>: <output>" echoes are never critical.
func (p *Packer) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "==> Builds finished") {
		return true
	}
	low := strings.ToLower(t)
	for _, s := range packerCritical {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// looksLikePacker reports whether b resembles `packer build` output.
func looksLikePacker(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "packer") ||
		strings.Contains(s, "==> ") ||
		strings.Contains(s, "amazon-ebs") ||
		strings.Contains(s, "Build '") ||
		strings.Contains(s, "Builds finished")
}

// isPackerBuilderProgress reports a per-step progress line of the form
// "==> <builder>: <step>" (the ": " after the arrow marks the builder
// prefix). These carry no result state and are dropped at Balanced+.
func isPackerBuilderProgress(t string) bool {
	const arrow = "==> "
	if !strings.HasPrefix(t, arrow) {
		return false
	}
	return strings.Contains(t[len(arrow):], ": ")
}

// Format compresses packer output; non-packer output falls back to generic.
func (p *Packer) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikePacker(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "packer: non-packer output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(p, raw, scrubbed, 0, "packer: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var progress []string // remaining "==> " progress pending Aggressive collapse
	dropped := 0

	flush := func() {
		if len(progress) == 0 {
			return
		}
		if level == LossAggressive {
			listing := strings.Join(progress, "\n")
			summary := fmt.Sprintf("  (+%d build steps)", len(progress))
			if len(summary) < len(listing) {
				kept = append(kept, summary)
				dropped += len(progress)
			} else {
				kept = append(kept, progress...)
			}
		} else {
			kept = append(kept, progress...)
		}
		progress = progress[:0]
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)

		// Critical lines survive at every level.
		if p.CriticalLine(t) {
			flush()
			kept = append(kept, line)
			continue
		}
		if t == "" {
			dropped++
			continue
		}
		// Balanced+: drop indented builder output echoes and the
		// "==> <builder>: <step>" per-step progress.
		if startsWithSpace(line) || isPackerBuilderProgress(t) {
			dropped++
			continue
		}
		// Remaining "==> " progress: kept at Balanced, collapsed at Aggressive.
		if strings.HasPrefix(t, "==> ") {
			if level == LossAggressive {
				progress = append(progress, line)
				continue
			}
			flush()
			kept = append(kept, line)
			continue
		}
		flush()
		kept = append(kept, line)
	}
	flush()

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("packer: %s, %d lines dropped", level, dropped)
	res := enforceCritical(p, raw, compact, dropped, notes)
	return res, true
}
