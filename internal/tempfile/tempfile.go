// Package tempfile creates the temporary files that atomic writers rename
// into place, and clears the ones an earlier writer left behind.
//
// An atomic writer creates a temporary file beside its target, writes it,
// and renames it over the target, removing the temporary file on any
// failure. A process killed between the create and the rename — a status
// line or hook that Claude Code cancels, a CLI interrupted — never runs
// that removal, and its temporary file stays forever. Create removes those
// leftovers on the next write.
package tempfile

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StaleAfter is how old a leftover must be before Create removes it. A
// live writer holds its temporary file for milliseconds; a minute leaves
// any concurrent writer's file alone with a wide margin.
const StaleAfter = time.Minute

// Create is os.CreateTemp(dir, pattern) that first removes, best effort,
// every regular file in dir that os.CreateTemp could have named from the
// same pattern and that was last modified more than StaleAfter ago. Only
// names of the pattern's own shape are touched: the prefix, a run of
// decimal digits (os.CreateTemp's random part), then the suffix.
func Create(dir, pattern string) (*os.File, error) {
	if dir != "" {
		sweep(dir, pattern, time.Now().Add(-StaleAfter))
	}
	return os.CreateTemp(dir, pattern)
}

// sweep removes the leftovers of pattern in dir modified before cutoff.
// Errors are ignored: a leftover that cannot be listed or removed now is
// tried again on the next write, and must never fail this one.
func sweep(dir, pattern string, cutoff time.Time) {
	prefix, suffix := split(pattern)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !matches(e.Name(), prefix, suffix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// split divides pattern as os.CreateTemp does: the random part replaces
// the last "*", or is appended when there is none.
func split(pattern string) (prefix, suffix string) {
	if i := strings.LastIndexByte(pattern, '*'); i >= 0 {
		return pattern[:i], pattern[i+1:]
	}
	return pattern, ""
}

// matches reports whether name is prefix, one or more decimal digits,
// then suffix.
func matches(name, prefix, suffix string) bool {
	if len(name) <= len(prefix)+len(suffix) ||
		!strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	for _, c := range name[len(prefix) : len(name)-len(suffix)] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
