package formatter

import (
	"fmt"
	"strings"
)

// Gcloud compresses the output of `gcloud` CLI commands. Like the other
// cloud CLIs it emits JSON (`--format=json`) and table/text output:
//
//   - JSON is passed through byte for byte — the generic scrub's consecutive
//     duplicate-line removal would corrupt a JSON array's repeated structural
//     lines. Any output whose first non-whitespace byte is `{` or `[` is left
//     untouched.
//   - Table / text output has ASCII border decoration and progress chatter
//     ("Waiting for operation…") stripped at Balanced+; header and data rows
//     survive. Any line carrying an error signal (error, failed, denied,
//     does not exist, not found, Exception) is critical and always kept.
//
// Output that does not resemble gcloud is handed to the generic noise scrub.
type Gcloud struct{}

// NewGcloud returns the gcloud formatter.
func NewGcloud() *Gcloud { return &Gcloud{} }

// Command reports the gcloud command token.
func (g *Gcloud) Command() string { return "gcloud" }

// CriticalLine treats a cloud-CLI error line as critical; table rows are kept
// structurally but not force-critical.
func (g *Gcloud) CriticalLine(line string) bool { return containsCloudError(line) }

// looksLikeGcloud reports whether b resembles gcloud table/text output or
// carries the literal command token.
func looksLikeGcloud(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "gcloud") ||
		strings.Contains(s, "PROJECT_ID") ||
		strings.Contains(s, "NAME  ") ||
		strings.Contains(s, "Listed 0 items") ||
		strings.Contains(s, "Created [")
}

// Format compresses gcloud output. JSON is passed through; non-gcloud output
// falls back to the generic scrub; table/text output has border and progress
// decoration stripped at Balanced+.
func (g *Gcloud) Format(raw []byte, level LossLevel) (Result, bool) {
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
			Notes:        "gcloud: json passthrough",
		}, true
	}
	if !looksLikeGcloud(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "gcloud: non-gcloud output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(g, raw, scrubbed, 0, "gcloud: conservative scrub")
		return res, true
	}
	compact, dropped := stripCloudDecoration(g, scrubbed)
	notes := fmt.Sprintf("gcloud: %s, %d lines dropped", level, dropped)
	res := enforceCritical(g, raw, compact, dropped, notes)
	return res, true
}
