package routeguard

import (
	"strings"
	"testing"
	"time"
)

// With the plan running short, the per-turn advice moves edits and deep
// work a tier further down, as subagent routing already does, and says
// why.
func TestEvaluateReadsPlanPressure(t *testing.T) {
	pressed := &Pressure{Vendor: "Claude", Window: "weekly", LeftPct: 12, RunsOutBeforeReset: 30 * time.Hour}
	in := input(t, "fix the typo in the README", "claude-opus-5", ModeAdvise)
	if got := Evaluate(in); got.Advise {
		t.Fatalf("an edit on the default tier advised without pressure: %+v", got)
	}
	in = input(t, "fix the typo in the README", "claude-opus-5", ModeAdvise)
	in.Pressure = pressed
	got := Evaluate(in)
	if !got.Advise || got.To != "claude-sonnet-5" {
		t.Fatalf("pressed edit: %+v", got)
	}
	if !strings.Contains(got.Reason, "runs out") {
		t.Errorf("reason = %q", got.Reason)
	}
	// Lookups already sit where they need to; pressure changes nothing.
	in = input(t, "show me the retention config", "claude-opus-5", ModeAdvise)
	in.Pressure = pressed
	if got := Evaluate(in); got.To != "claude-haiku-4-5" {
		t.Errorf("pressed lookup: %+v", got)
	}
}

// A window that will not last to its reset at the pace so far says when
// it runs out, not only what is left.
func TestHeadroomNoteSaysWhenAWindowRunsOut(t *testing.T) {
	got := HeadroomNote([]NoteWindow{
		// 62% used with 2h40 of 5h gone: at that rate the rest lasts 98
		// minutes, and the reset is 140 minutes away.
		{Name: "5h", LeftPct: 38, Duration: 5 * time.Hour, ResetsAt: noteNow.Add(2*time.Hour + 20*time.Minute)},
		{Name: "week", LeftPct: 61, Duration: 7 * 24 * time.Hour, ResetsAt: noteNow.Add(3*24*time.Hour + 9*time.Hour)},
	}, noteNow, time.UTC)
	if !strings.Contains(got, "5h window 38% left (runs out ~13:38 at this pace, resets 14:20)") {
		t.Errorf("note = %q", got)
	}
	if len(got) > MaxNoteLen {
		t.Errorf("note is %d characters, over %d: %q", len(got), MaxNoteLen, got)
	}
}

// Running out is said when it starts, even within a step.
func TestHeadroomNoteIsSaidAgainWhenAWindowStartsRunningOut(t *testing.T) {
	dir := t.TempDir()
	lasting := []NoteWindow{{Name: "5h", LeftPct: 40, Duration: 5 * time.Hour, ResetsAt: noteNow.Add(time.Hour)}}
	if _, ok := DecideHeadroomNote(dir, "s", lasting, noteNow, time.UTC); !ok {
		t.Fatal("first note not said")
	}
	short := []NoteWindow{{Name: "5h", LeftPct: 38, Duration: 5 * time.Hour, ResetsAt: noteNow.Add(2*time.Hour + 20*time.Minute)}}
	if note, ok := DecideHeadroomNote(dir, "s", short, noteNow, time.UTC); !ok || !strings.Contains(note, "runs out") {
		t.Fatalf("running out not said: %q %v", note, ok)
	}
}
