package formatter

import (
	"fmt"
	"strings"
)

// Alembic compresses the output of `alembic upgrade`/`downgrade`. The
// "Running upgrade …" / "Running downgrade …" lines describe the actual
// migration an agent acts on and are always kept, together with any error
// or traceback line. The boilerplate INFO lines the runtime prints on every
// run ("Context impl …", "Will assume transactional DDL.") carry no state
// and are stripped at Balanced and above.
//
// Only migration-style output is transformed. Any other alembic output that
// reaches this formatter is handed to the generic noise scrub, so the
// formatter is never destructive on output it does not model.
type Alembic struct{}

// NewAlembic returns the alembic formatter.
func NewAlembic() *Alembic { return &Alembic{} }

// Command reports the alembic command token.
func (a *Alembic) Command() string { return "alembic" }

// CriticalLine treats the state-bearing migration lines and every error or
// traceback line as critical: the "Running upgrade …" / "Running downgrade
// …" entries (the actual migration) and any line signalling an error
// (Error/error/FAILED/Traceback/".exc."). The boilerplate INFO lines are
// never critical.
func (a *Alembic) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.Contains(t, "Running upgrade"),
		strings.Contains(t, "Running downgrade"),
		strings.Contains(t, "Error"),
		strings.Contains(t, "error"),
		strings.Contains(t, "FAILED"),
		strings.Contains(t, "Traceback"),
		strings.Contains(t, ".exc."):
		return true
	}
	return false
}

// looksLikeAlembic reports whether b resembles alembic migration output.
func looksLikeAlembic(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "alembic") ||
		strings.Contains(s, "Running upgrade") ||
		strings.Contains(s, "Running downgrade") ||
		strings.Contains(s, "runtime.migration") ||
		strings.Contains(s, "revision")
}

// alembicBoilerplate reports the per-run INFO lines that carry no migration
// state and are safe to drop at Balanced+.
func alembicBoilerplate(t string) bool {
	switch {
	case strings.Contains(t, "Context impl"),
		strings.Contains(t, "Will assume transactional DDL"):
		return true
	}
	return false
}

// Format compresses alembic output. Non-migration output falls back to the
// generic scrub.
func (a *Alembic) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeAlembic(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "alembic: non-migration output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(a, raw, scrubbed, 0, "alembic: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if a.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if alembicBoilerplate(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("alembic: %s, %d lines dropped", level, dropped)
	res := enforceCritical(a, raw, compact, dropped, notes)
	return res, true
}
