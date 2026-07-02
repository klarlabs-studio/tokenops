package formatter

import (
	"fmt"
	"strings"
)

// Az compresses the output of `az` (Azure CLI) commands. Like the other cloud
// CLIs it emits JSON (the default output) and table/text output:
//
//   - JSON is passed through byte for byte — the generic scrub's consecutive
//     duplicate-line removal would corrupt a JSON array's repeated structural
//     lines. Any output whose first non-whitespace byte is `{` or `[` is left
//     untouched.
//   - Table / text output (`-o table`) has ASCII border decoration (the
//     "------  ------" rule under the header) and progress chatter stripped at
//     Balanced+; header and data rows survive. Any line carrying an error
//     signal (error, failed, denied, does not exist, not found, Exception) is
//     critical and always kept.
//
// Output that does not resemble az is handed to the generic noise scrub.
type Az struct{}

// NewAz returns the az formatter.
func NewAz() *Az { return &Az{} }

// Command reports the az command token.
func (a *Az) Command() string { return "az" }

// CriticalLine treats a cloud-CLI error line as critical; table rows are kept
// structurally but not force-critical.
func (a *Az) CriticalLine(line string) bool { return containsCloudError(line) }

// looksLikeAz reports whether b resembles az table/text output or carries the
// literal command token.
func looksLikeAz(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "az ") ||
		strings.Contains(s, "AppId") ||
		strings.Contains(s, "resourceGroup") ||
		strings.Contains(s, "----")
}

// Format compresses az output. JSON is passed through; non-az output falls
// back to the generic scrub; table/text output has border and progress
// decoration stripped at Balanced+.
func (a *Az) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if isCloudJSON(scrubbed) {
		return Result{
			Compact:      raw,
			BytesBefore:  len(raw),
			BytesAfter:   len(raw),
			CriticalKept: true,
			Notes:        "az: json passthrough",
		}, true
	}
	if !looksLikeAz(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "az: non-az output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(a, raw, scrubbed, 0, "az: conservative scrub")
		return res, true
	}
	compact, dropped := stripCloudDecoration(a, scrubbed)
	notes := fmt.Sprintf("az: %s, %d lines dropped", level, dropped)
	res := enforceCritical(a, raw, compact, dropped, notes)
	return res, true
}
