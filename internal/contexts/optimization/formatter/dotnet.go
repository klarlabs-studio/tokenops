package formatter

import (
	"fmt"
	"strings"
)

// Dotnet compresses the output of `dotnet build` and `dotnet test`. A build
// restores packages ("Determining projects to restore…", "Restored …"), emits
// a project-output pointer per artifact ("MyProj -> /bin/…/MyProj.dll"), then
// prints compiler diagnostics, the "Build succeeded."/"Build FAILED." result,
// and a "N Warning(s)"/"N Error(s)" summary. `dotnet test` prints a
// "Passed!  - Failed: 0, Passed: 42, …" or "Failed!  - Failed: 2, …" line.
//
// Compiler errors ("): error CSxxxx"), the "Build FAILED." result, the test
// "Failed!" line, and the "Error(s)"/"Failed:" summaries are the signal an
// agent acts on and are always kept. The restore chatter and artifact pointers
// are stripped at Balanced and above; at Aggressive the advisory
// "): warning …" diagnostics are dropped as well.
//
// Output that does not resemble dotnet is handed to the generic noise scrub,
// so the formatter is never destructive on commands it does not model.
type Dotnet struct{}

// NewDotnet returns the dotnet formatter.
func NewDotnet() *Dotnet { return &Dotnet{} }

// Command reports the dotnet command token.
func (d *Dotnet) Command() string { return "dotnet" }

// CriticalLine treats compiler errors and failure/result signal as critical:
// any "): error " diagnostic, the "Build FAILED" result, the test "Failed!"
// line, and the "Error(s)"/"Failed:" summaries. Advisory "): warning "
// diagnostics and the "Build succeeded." result are never critical.
func (d *Dotnet) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.Contains(t, "): error "),
		strings.Contains(t, "Build FAILED"),
		strings.HasPrefix(t, "Failed!"),
		strings.Contains(t, "Error(s)"),
		strings.Contains(t, "Failed:"):
		return true
	}
	return false
}

// looksLikeDotnet reports whether b resembles dotnet build/test output.
func looksLikeDotnet(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Restored ") ||
		strings.Contains(s, "Build succeeded") ||
		strings.Contains(s, "Build FAILED") ||
		strings.Contains(s, "dotnet") ||
		strings.Contains(s, " -> ") ||
		strings.Contains(s, "Determining projects")
}

// isDotnetRestoreNoise reports the restore chatter and per-artifact build
// pointers dropped at Balanced+: the "Determining projects to restore" banner,
// "Restored …" lines, and "MyProj -> /bin/…" artifact pointers.
func isDotnetRestoreNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Determining projects to restore"),
		strings.HasPrefix(t, "Restored "),
		strings.Contains(t, " -> "):
		return true
	}
	return false
}

// Format compresses dotnet output; non-dotnet output falls back to generic.
func (d *Dotnet) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeDotnet(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "dotnet: non-dotnet output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(d, raw, scrubbed, 0, "dotnet: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if d.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isDotnetRestoreNoise(t) || t == "" {
			dropped++
			continue
		}
		if level == LossAggressive && strings.Contains(t, "): warning ") {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("dotnet: %s, %d lines dropped", level, dropped)
	res := enforceCritical(d, raw, compact, dropped, notes)
	return res, true
}
