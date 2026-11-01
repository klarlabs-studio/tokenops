package readguard

import (
	"strings"
	"testing"
	"time"
)

func TestBashCall(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for cmd, want := range map[string]CallKind{
		"git status":             CallCommand,
		"git  status ":           CallCommand,
		"ls -la internal":        CallCommand,
		"git commit -m x":        CallEdit,
		"go test ./...":          CallEdit,
		"git status | head":      CallEdit,
		"rm -rf build":           CallEdit,
		"find . -name x -delete": CallEdit,
	} {
		if got := BashCall(cmd, "/repo", at).Kind; got != want {
			t.Errorf("BashCall(%q) = %s, want %s", cmd, got, want)
		}
	}
}

// A read-only command run again is a repeat only with nothing changed
// since, in the same directory, within RepeatWithin.
func TestObserveFindsARepeatedCommand(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cmd := func(c string, at time.Time) Call { return BashCall(c, "/repo", at) }
	cases := []struct {
		name  string
		calls []Call
		want  bool
	}{
		{"again with nothing between", []Call{cmd("git status", t0), cmd("git status", t0.Add(time.Minute))}, true},
		{"a read between", []Call{cmd("git status", t0), {Kind: CallRead, Path: "/repo/a.go"}, cmd("git status", t0.Add(time.Minute))}, true},
		{"an edit between", []Call{cmd("git status", t0), {Kind: CallEdit}, cmd("git status", t0.Add(time.Minute))}, false},
		{"a changing command between", []Call{cmd("git status", t0), cmd("go generate ./...", t0), cmd("git status", t0.Add(time.Minute))}, false},
		{"too long ago", []Call{cmd("git status", t0), cmd("git status", t0.Add(RepeatWithin+time.Second))}, false},
		{"another directory", []Call{cmd("git status", t0), BashCall("git status", "/other", t0.Add(time.Minute))}, false},
		{"another command", []Call{cmd("git status", t0), cmd("git diff", t0.Add(time.Minute))}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			var last Observation
			for _, call := range c.calls {
				last = Observe(dir, "s", "", call, nil, nil)
			}
			if (last.Repeat != nil) != c.want {
				t.Fatalf("repeat = %+v, want %v", last.Repeat, c.want)
			}
		})
	}
}

// The refusal names the command and when it ran, and a refused repeat
// does not move that time: the output in context is still the first.
func TestRepeatReasonAndTime(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	Observe(dir, "s", "", BashCall("git status", "/repo", t0), nil, nil)
	o := Observe(dir, "s", "", BashCall("git status", "/repo", t0.Add(2*time.Minute)), nil, nil)
	if o.Repeat == nil || o.Repeat.CallsAgo != 1 || o.Repeat.Ago != 2*time.Minute {
		t.Fatalf("repeat = %+v", o.Repeat)
	}
	if r := o.Repeat.Reason(); !strings.Contains(r, "`git status`") || !strings.Contains(r, "2m ago") ||
		!strings.Contains(r, "the previous tool call") {
		t.Errorf("reason = %q", r)
	}
	o = Observe(dir, "s", "", BashCall("git status", "/repo", t0.Add(5*time.Minute)), nil, nil)
	if o.Repeat == nil || o.Repeat.Ago != 5*time.Minute {
		t.Errorf("third run: %+v, want 5m since the first", o.Repeat)
	}
}

func TestRecordRepeatCountsSeparately(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	RecordRepeat(dir, "s", "", ModeObserve, now)
	RecordRepeat(dir, "s", "", ModeActive, now)
	s, err := ReadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.RepeatCommands != 2 || s.RepeatCommandsBlocked != 1 || s.Events != 0 || s.WouldBlock != 0 {
		t.Errorf("stats = %+v", s)
	}
}
