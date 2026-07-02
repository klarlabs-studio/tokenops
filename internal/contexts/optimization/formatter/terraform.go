package formatter

import (
	"fmt"
	"strings"
)

// Terraform compresses the output of `terraform plan` / `terraform apply`.
// The change-summary signal an agent acts on is the resource-address headers
// ("  # aws_instance.web will be created"), the action-verb lines ("will be
// created/updated in-place/replaced/destroyed"), the resource action markers
// ("~ resource …"), the "Plan:" summary, "Apply complete!", and any error —
// these are always kept.
//
// The per-resource attribute diffs dominate the output and are almost all
// noise: state-refresh chatter ("… : Refreshing state …"), read progress
// ("… : Reading …"), the unchanged-attribute lines inside a diff block (an
// indented "attr = value" with no +/-/~ marker), the "(known after apply)"
// filler, and provider-init chatter. These are stripped at Balanced and
// above, keeping only headers, action markers, and the changed (+/-/~)
// attribute lines. At Aggressive each resource diff body collapses to just
// its "# " header plus its changed attribute lines, dropping the surrounding
// context (preamble, braces, legends) entirely.
//
// Output that does not resemble terraform is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not model.
type Terraform struct{}

// NewTerraform returns the terraform formatter.
func NewTerraform() *Terraform { return &Terraform{} }

// Command reports the terraform command token.
func (t *Terraform) Command() string { return "terraform" }

// CriticalLine treats the change-summary and error signal as critical: the
// resource-address headers ("# …"), the action-verb lines ("will be
// created/destroyed/updated in-place/replaced"), the resource action markers
// ("+ resource", "- resource", "~ resource", "-/+ resource"), the "Plan:"
// summary, "Apply complete!", and any error line. "Warning:" is never
// critical.
func (t *Terraform) CriticalLine(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	switch {
	case strings.HasPrefix(s, "# "),
		strings.HasPrefix(s, "+ resource"),
		strings.HasPrefix(s, "- resource"),
		strings.HasPrefix(s, "~ resource"),
		strings.HasPrefix(s, "-/+ resource"),
		strings.HasPrefix(s, "Plan:"),
		strings.HasPrefix(s, "Apply complete!"),
		strings.HasPrefix(s, "Error"),
		strings.Contains(s, "Error:"),
		hasTerraformActionVerb(s):
		return true
	}
	return false
}

// hasTerraformActionVerb reports the resource-action phrasing terraform
// prints on the "# …" header line, the signal that names what will change.
func hasTerraformActionVerb(s string) bool {
	return strings.Contains(s, "will be created") ||
		strings.Contains(s, "will be destroyed") ||
		strings.Contains(s, "will be updated in-place") ||
		strings.Contains(s, "will be replaced")
}

// looksLikeTerraform reports whether b resembles terraform plan/apply output.
func looksLikeTerraform(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Terraform will perform") ||
		strings.Contains(s, "Plan:") ||
		strings.Contains(s, "# ") ||
		strings.Contains(s, "terraform") ||
		strings.Contains(s, "Apply complete") ||
		strings.Contains(s, "Refreshing state")
}

// isTerraformRefreshing reports the state-refresh / read progress lines
// terraform prints per resource before the diff. They carry no plan state and
// are safe to drop at Balanced+.
func isTerraformRefreshing(s string) bool {
	return strings.Contains(s, ": Refreshing state...") ||
		strings.Contains(s, ": Reading...")
}

// isTerraformProviderChatter reports the backend/provider initialization lines
// that carry no plan state and are safe to drop at Balanced+.
func isTerraformProviderChatter(s string) bool {
	switch {
	case strings.HasPrefix(s, "Initializing"),
		strings.HasPrefix(s, "- Installing"),
		strings.HasPrefix(s, "- Installed"),
		strings.HasPrefix(s, "- Reusing"),
		strings.HasPrefix(s, "- Using"),
		strings.HasPrefix(s, "- Finding"),
		strings.HasPrefix(s, "- Downloading"),
		strings.HasPrefix(s, "Terraform has been successfully initialized"):
		return true
	}
	return false
}

// isTerraformChangedAttr reports the changed-attribute lines inside a diff
// block: an indented marker (+, -, ~, -/+, +/-) followed by an "attr = value"
// assignment. Requiring the "=" keeps the check from swallowing the symbols
// legend ("~ update in-place") that shares the marker prefix but has no
// assignment.
func isTerraformChangedAttr(s string) bool {
	hasMarker := strings.HasPrefix(s, "+ ") ||
		strings.HasPrefix(s, "- ") ||
		strings.HasPrefix(s, "~ ") ||
		strings.HasPrefix(s, "-/+ ") ||
		strings.HasPrefix(s, "+/- ")
	if !hasMarker {
		return false
	}
	return strings.Contains(s, "=")
}

// isTerraformUnchangedAttr reports the unchanged-attribute lines inside a diff
// block: an indented "attr = value" assignment with NO leading +/-/~ marker.
// These dominate a noisy plan and are dropped at Balanced+.
func isTerraformUnchangedAttr(line string) bool {
	if line == "" || (line[0] != ' ' && line[0] != '\t') {
		return false // must be indented to sit inside a diff block
	}
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "# ") || isTerraformChangedAttr(s) {
		return false
	}
	return strings.Contains(s, "=")
}

// Format compresses terraform output; non-terraform output falls back to
// generic.
func (t *Terraform) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeTerraform(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "terraform: non-terraform output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(t, raw, scrubbed, 0, "terraform: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if t.CriticalLine(s) {
			kept = append(kept, line)
			continue
		}
		// Noise dropped at Balanced+.
		if isTerraformRefreshing(s) ||
			isTerraformProviderChatter(s) ||
			strings.Contains(s, "(known after apply)") ||
			isTerraformUnchangedAttr(line) ||
			s == "" {
			dropped++
			continue
		}
		// Changed attribute lines are the state-bearing diff; keep them.
		if isTerraformChangedAttr(s) {
			kept = append(kept, line)
			continue
		}
		// Remaining context (preamble, braces, legends): kept at Balanced,
		// dropped at Aggressive so each diff body collapses to header +
		// changed attributes.
		if level == LossAggressive {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("terraform: %s, %d lines dropped", level, dropped)
	res := enforceCritical(t, raw, compact, dropped, notes)
	return res, true
}
