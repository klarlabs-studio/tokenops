package formatter

import (
	"fmt"
	"strings"
)

// Flyway compresses the output of `flyway migrate` (Community/Teams). The
// lines that describe the actual schema change an agent acts on — the
// "Migrating schema to version …" entries, the "Successfully applied …"
// summary, and any error/failure lines — are always kept. The surrounding
// banner ("Flyway … by Redgate"), the JDBC "Database:" line, the
// "Successfully validated …" line, and the "Current version …" line are
// noise stripped at Balanced and above.
//
// Only migrate-style output is transformed. Any other flyway output that
// reaches this formatter is handed to the generic noise scrub, so the
// formatter is never destructive on output it does not model.
type Flyway struct{}

// NewFlyway returns the flyway formatter.
func NewFlyway() *Flyway { return &Flyway{} }

// Command reports the flyway command token.
func (f *Flyway) Command() string { return "flyway" }

// CriticalLine treats the state-bearing migration lines and every error or
// failure line as critical: the "Migrating schema to version …" entries
// (the actual change), the "Successfully applied …" summary, error lines
// ("ERROR"/"Error"), and any line reporting a failure. Banner, database,
// and validation lines are never critical.
func (f *Flyway) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "ERROR"),
		strings.HasPrefix(t, "Error"),
		strings.HasPrefix(t, "Migrating schema to version"),
		strings.HasPrefix(t, "Successfully applied"),
		strings.Contains(t, "failed"):
		return true
	}
	return false
}

// looksLikeFlyway reports whether b resembles `flyway migrate` output.
func looksLikeFlyway(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "Flyway") ||
		strings.Contains(s, "Migrating schema") ||
		strings.Contains(s, "flyway") ||
		strings.Contains(s, "migration") ||
		strings.Contains(s, "Schema version")
}

// flywayNoise reports the banner/database/validation lines that carry no
// state and are safe to drop at Balanced+.
func flywayNoise(t string) bool {
	switch {
	case strings.HasPrefix(t, "Flyway") && strings.Contains(t, "by Redgate"),
		strings.HasPrefix(t, "Database:"),
		strings.HasPrefix(t, "Successfully validated"),
		strings.HasPrefix(t, "Current version"):
		return true
	}
	return false
}

// Format compresses flyway output. Non-migrate output falls back to the
// generic scrub.
func (f *Flyway) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeFlyway(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "flyway: non-migrate output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(f, raw, scrubbed, 0, "flyway: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if f.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if flywayNoise(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("flyway: %s, %d lines dropped", level, dropped)
	res := enforceCritical(f, raw, compact, dropped, notes)
	return res, true
}
