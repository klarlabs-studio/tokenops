package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/statuslinepace"
)

// The line shows the daemon's run-out forecast for the window on it, from
// the file the daemon keeps, and nothing when that file is stale, missing,
// malformed, about another window or says the window lasts.
func TestStatuslineShowsTheDaemonsPace(t *testing.T) {
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.Local)
	reset := now.Add(72 * time.Hour).Truncate(time.Second)
	input := fmt.Sprintf(`{"rate_limits": {"seven_day": {"used_percentage": 26, "resets_at": %d}}}`, reset.Unix())
	out := now.Add(30 * time.Hour)
	want := "out ~" + out.Format("Mon 15:04")
	file := func(computed time.Time, resets, runsOut time.Time) statuslinepace.File {
		return statuslinepace.File{ComputedAt: computed, Windows: []statuslinepace.Window{
			{Provider: "anthropic", Name: "week", ResetsAt: resets, RunsOutAt: runsOut, Rate: "at the last hour's rate"},
		}}
	}
	cases := []struct {
		name  string
		file  *statuslinepace.File
		raw   string
		shown bool
	}{
		{name: "fresh, runs out before its reset", file: ptr(file(now.Add(-2*time.Minute), reset, out)), shown: true},
		{name: "stale", file: ptr(file(now.Add(-statuslinepace.FreshFor-time.Minute), reset, out))},
		{name: "missing"},
		{name: "malformed", raw: "{oops"},
		{name: "another window", file: ptr(file(now, reset.Add(-7*24*time.Hour), out.Add(-7*24*time.Hour)))},
		{name: "lasts to its reset", file: ptr(file(now, reset, time.Time{}))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("NO_COLOR", "1")
			path := filepath.Join(home, ".tokenops", statuslinepace.FileName)
			switch {
			case c.file != nil:
				if err := statuslinepace.Write(path, *c.file); err != nil {
					t.Fatal(err)
				}
			case c.raw != "":
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(c.raw), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lines := statusLinesAt([]byte(input), now)
			if len(lines) != 1 || !strings.Contains(lines[0], "wk 74% left") {
				t.Fatalf("line %q", lines)
			}
			if got := strings.Contains(lines[0], want); got != c.shown {
				t.Errorf("line %q: forecast shown %v, want %v", lines[0], got, c.shown)
			}
			if !c.shown && strings.Contains(lines[0], "out ~") {
				t.Errorf("line %q shows a forecast", lines[0])
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
