package formatter

import (
	"fmt"
	"strings"
)

// Aws compresses the output of `aws` CLI commands. The AWS CLI emits two
// broad output shapes an agent acts on:
//
//   - JSON (the default `--output json`). JSON is never compressed: the
//     generic scrub dedupes consecutive identical lines, which corrupts a
//     JSON array whose elements repeat structural `{` / `}` lines. Any output
//     whose first non-whitespace byte is `{` or `[` is passed through byte
//     for byte.
//   - Table / text (`--output table` / `--output text`). ASCII table border
//     decoration ("+----+----+" separators, pure-dash rules) and progress /
//     spinner chatter are noise dropped at Balanced+; header and data rows
//     survive. Any line carrying an AWS error signal (error, failed, denied,
//     AccessDenied, does not exist, not found, Exception) is critical and
//     always kept.
//
// Output that does not resemble aws is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type Aws struct{}

// NewAws returns the aws formatter.
func NewAws() *Aws { return &Aws{} }

// Command reports the aws command token.
func (a *Aws) Command() string { return "aws" }

// cloudErrorSignals are the substrings (matched case-insensitively) that mark
// a cloud-CLI line as an error the agent must not lose. Cloud CLIs surface
// failures as prose, so the predicate keys on that prose rather than on any
// table structure.
var cloudErrorSignals = []string{
	"error",
	"failed",
	"denied",
	"accessdenied",
	"does not exist",
	"not found",
	"exception",
}

// containsCloudError reports whether a line carries a cloud-CLI error signal.
func containsCloudError(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if l == "" {
		return false
	}
	for _, s := range cloudErrorSignals {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// CriticalLine treats a cloud-CLI error line as critical. Table rows are kept
// structurally (they are not decoration) but are not force-critical: the
// error prose is the signal the agent acts on.
func (a *Aws) CriticalLine(line string) bool { return containsCloudError(line) }

// isCloudJSON reports whether the first non-whitespace byte of b is `{` or
// `[`, i.e. b is JSON that must be passed through untouched.
func isCloudJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

// isCloudBorder reports a pure ASCII table-border line (only '+', '-', '|' and
// spaces, with at least one border glyph). These carry no state and are
// dropped at Balanced+.
func isCloudBorder(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		if !strings.ContainsRune("+-| ", r) {
			return false
		}
	}
	return strings.ContainsAny(t, "+-|")
}

// isCloudProgress reports a spinner / progress chatter line ("Waiting for…",
// "Running…", or a run of dots). These are dropped at Balanced+.
func isCloudProgress(t string) bool {
	if strings.HasPrefix(t, "Waiting for") || strings.HasPrefix(t, "Running") {
		return true
	}
	return t != "" && strings.Trim(t, ".") == ""
}

// looksLikeAws reports whether b resembles aws CLI table/text output or
// carries the literal command token.
func looksLikeAws(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "aws") ||
		strings.Contains(s, "arn:") ||
		strings.Contains(s, "InstanceId") ||
		strings.Contains(s, "----") ||
		strings.Contains(s, "RoleId")
}

// Format compresses aws output. JSON is passed through; non-aws output falls
// back to the generic scrub; table/text output has border and progress
// decoration stripped at Balanced+.
func (a *Aws) Format(raw []byte, level LossLevel) (Result, bool) {
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
			Notes:        "aws: json passthrough",
		}, true
	}
	if !looksLikeAws(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "aws: non-aws output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(a, raw, scrubbed, 0, "aws: conservative scrub")
		return res, true
	}
	compact, dropped := stripCloudDecoration(a, scrubbed)
	notes := fmt.Sprintf("aws: %s, %d lines dropped", level, dropped)
	res := enforceCritical(a, raw, compact, dropped, notes)
	return res, true
}

// stripCloudDecoration drops ASCII table borders, progress chatter, and blank
// lines from scrubbed cloud-CLI output while keeping every critical line,
// header, and data row. It is shared by the aws / gcloud / az formatters at
// Balanced and above; the behaviour is identical at Aggressive so output
// never grows past Balanced.
func stripCloudDecoration(f Formatter, scrubbed []byte) ([]byte, int) {
	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if f.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if t == "" || isCloudBorder(t) || isCloudProgress(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	return []byte(strings.Join(trimBlanks(kept), "\n")), dropped
}
