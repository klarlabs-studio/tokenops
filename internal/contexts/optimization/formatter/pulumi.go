package formatter

import (
	"fmt"
	"strings"
)

// Pulumi compresses the output of `pulumi preview` / `pulumi up`. The
// change-summary signal an agent acts on is the resource-operation lines in
// the update tree ("+ aws:s3/bucket:Bucket my-bucket create", "~ …
// update", "- … delete", "+- … replace"), the changed-property diff lines
// ("~ instanceType: old => new"), the "Resources:" summary and its count
// lines ("+ 2 to create", "~ 1 to update", "- 1 to delete"), the "@ …"
// progress markers pulumi emits around each preview, and any error — these
// are always kept.
//
// The per-resource property listing dominates the output and is almost all
// noise: the unchanged-property lines inside a diff block (an indented
// "attr: value" with no +/-/~ marker), the "Diagnostics:" boilerplate
// header, and blank decoration. These are stripped at Balanced and above,
// keeping only the operation lines, changed-property lines, the summary, the
// "@ " markers, and errors. At Aggressive each resource diff body collapses
// to just its operation line plus its changed-property lines, dropping the
// surrounding context (preamble, tree header, metadata, outputs) entirely.
//
// Output that does not resemble pulumi is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not
// model.
type Pulumi struct{}

// NewPulumi returns the pulumi formatter.
func NewPulumi() *Pulumi { return &Pulumi{} }

// Command reports the pulumi command token.
func (p *Pulumi) Command() string { return "pulumi" }

// CriticalLine treats the change-summary and error signal as critical: the
// resource-operation lines ("+ /- /~ /+- " followed by a resource type,
// i.e. a marked line containing ":"), the "Resources:" summary header and
// its count lines ("+ N to create", "~ N to update", "- N to delete"), the
// "@ " progress markers, and any error line ("error…", "Error…", or a line
// mentioning "failed"). Unchanged/context lines are never critical.
func (p *Pulumi) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	switch {
	case strings.HasPrefix(s, "error"),
		strings.HasPrefix(s, "Error"),
		strings.Contains(s, "failed"),
		strings.HasPrefix(s, "Resources:"),
		strings.HasPrefix(s, "@ "),
		isPulumiCountLine(s),
		isPulumiResourceOp(s):
		return true
	}
	return false
}

// hasPulumiDiffMarker reports whether a trimmed line carries one of the diff
// operation markers pulumi prefixes onto resource-operation and changed
// property lines.
func hasPulumiDiffMarker(s string) bool {
	return strings.HasPrefix(s, "+ ") ||
		strings.HasPrefix(s, "- ") ||
		strings.HasPrefix(s, "~ ") ||
		strings.HasPrefix(s, "+- ")
}

// isPulumiResourceOp reports the resource-operation lines in the update
// tree: a diff marker followed by a resource type token (which always
// contains a ":", e.g. "aws:s3/bucket:Bucket"). Changed-property lines
// ("~ instanceType: …") also match this shape; both are state-bearing and
// must survive, so treating them uniformly as critical is intentional.
func isPulumiResourceOp(s string) bool {
	if !hasPulumiDiffMarker(s) {
		return false
	}
	return strings.Contains(s, ":")
}

// isPulumiCountLine reports the per-action count lines under the
// "Resources:" summary ("+ N to create", "~ N to update", "- N to delete").
func isPulumiCountLine(s string) bool {
	if !hasPulumiDiffMarker(s) {
		return false
	}
	return strings.Contains(s, " to create") ||
		strings.Contains(s, " to update") ||
		strings.Contains(s, " to delete")
}

// looksLikePulumi reports whether b resembles pulumi preview/up output.
func looksLikePulumi(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Previewing update") ||
		strings.Contains(s, "pulumi") ||
		strings.Contains(s, "Resources:") ||
		strings.Contains(s, "aws:") ||
		strings.Contains(s, "@ ") ||
		strings.Contains(s, " to create") ||
		strings.Contains(s, " to update")
}

// isPulumiDiagnosticsBoilerplate reports the "Diagnostics:" section header
// pulumi prints above per-resource error detail. The header itself carries
// no state (the error lines under it are kept via CriticalLine) and is safe
// to drop at Balanced+.
func isPulumiDiagnosticsBoilerplate(s string) bool {
	return s == "Diagnostics:"
}

// isPulumiUnchangedProp reports the unchanged-property lines inside a diff
// block: an indented "attr: value" with NO leading +/-/~ marker. These
// dominate a noisy preview and are dropped at Balanced+. Requiring the ":"
// keeps the check scoped to property assignments rather than arbitrary
// context (braces, metadata like "[id=…]").
func isPulumiUnchangedProp(line string) bool {
	if line == "" || (line[0] != ' ' && line[0] != '\t') {
		return false // must be indented to sit inside a diff block
	}
	s := strings.TrimSpace(line)
	if s == "" || hasPulumiDiffMarker(s) {
		return false
	}
	return strings.Contains(s, ":")
}

// Format compresses pulumi output; non-pulumi output falls back to generic.
func (p *Pulumi) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikePulumi(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "pulumi: non-pulumi output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(p, raw, scrubbed, 0, "pulumi: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if p.CriticalLine(s) {
			kept = append(kept, line)
			continue
		}
		// Noise dropped at Balanced+.
		if isPulumiUnchangedProp(line) ||
			isPulumiDiagnosticsBoilerplate(s) ||
			s == "" {
			dropped++
			continue
		}
		// Any remaining marked line is a state-bearing diff entry; keep it.
		if hasPulumiDiffMarker(s) {
			kept = append(kept, line)
			continue
		}
		// Remaining context (preamble, tree header, metadata, outputs): kept
		// at Balanced, dropped at Aggressive so each diff body collapses to
		// its operation line plus changed properties.
		if level == LossAggressive {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("pulumi: %s, %d lines dropped", level, dropped)
	res := enforceCritical(p, raw, compact, dropped, notes)
	return res, true
}
