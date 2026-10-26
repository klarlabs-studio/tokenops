package routeguard

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

var noteNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func TestHeadroomNoteReadsLikeOneLine(t *testing.T) {
	got := HeadroomNote([]NoteWindow{
		{Name: "5h", LeftPct: 38, ResetsAt: noteNow.Add(2*time.Hour + 20*time.Minute)},
		{Name: "week", LeftPct: 61, ResetsAt: noteNow.Add(3*24*time.Hour + 9*time.Hour)},
	}, noteNow, time.UTC)
	want := "Plan headroom: 5h window 38% left (resets 14:20), week 61% left (resets Wed 21:00)."
	if got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	if len(got) > MaxNoteLen {
		t.Errorf("note is %d characters, over %d", len(got), MaxNoteLen)
	}
}

// Only the binding window is named, plus another close enough to bind
// next; a comfortable window is noise.
func TestHeadroomNoteNamesOnlyTheBindingWindows(t *testing.T) {
	for name, tc := range map[string]struct {
		ws   []NoteWindow
		want string
	}{
		"week binds, 5h comfortable": {
			[]NoteWindow{{Name: "5h", LeftPct: 90}, {Name: "week", LeftPct: 30}},
			"Plan headroom: week 30% left.",
		},
		"5h binds, week close": {
			[]NoteWindow{{Name: "week", LeftPct: 61}, {Name: "5h", LeftPct: 38}},
			"Plan headroom: 5h window 38% left, week 61% left.",
		},
		"one window": {
			[]NoteWindow{{Name: "5h", LeftPct: 4.6}},
			"Plan headroom: 5h window 5% left.",
		},
		"nothing known": {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := HeadroomNote(tc.ws, noteNow, time.UTC); got != tc.want {
				t.Errorf("note = %q, want %q", got, tc.want)
			}
		})
	}
}

// The note is said once per session and again only when a shown window
// crosses a step (50, 25, 10% left) or the binding window changes, so
// the conversation does not gain a line every turn.
func TestHeadroomNoteSpeaksOnlyOnAStep(t *testing.T) {
	dir := t.TempDir()
	steps := []struct {
		ws   []NoteWindow
		emit bool
	}{
		{[]NoteWindow{{Name: "5h", LeftPct: 70}, {Name: "week", LeftPct: 80}}, true},  // first turn
		{[]NoteWindow{{Name: "5h", LeftPct: 62}, {Name: "week", LeftPct: 79}}, false}, // same steps
		{[]NoteWindow{{Name: "5h", LeftPct: 48}, {Name: "week", LeftPct: 78}}, true},  // 5h crossed 50
		{[]NoteWindow{{Name: "5h", LeftPct: 30}, {Name: "week", LeftPct: 77}}, false},
		{[]NoteWindow{{Name: "5h", LeftPct: 24}, {Name: "week", LeftPct: 77}}, true}, // crossed 25
		{[]NoteWindow{{Name: "5h", LeftPct: 9}, {Name: "week", LeftPct: 76}}, true},  // crossed 10
		{[]NoteWindow{{Name: "5h", LeftPct: 8}, {Name: "week", LeftPct: 76}}, false},
		{[]NoteWindow{{Name: "5h", LeftPct: 100}, {Name: "week", LeftPct: 40}}, true}, // reset: week binds now
	}
	for i, s := range steps {
		note, emit := DecideHeadroomNote(dir, "s1", s.ws, noteNow, time.UTC)
		if emit != s.emit {
			t.Fatalf("turn %d: emit = %v (%q), want %v", i, emit, note, s.emit)
		}
		if emit && !strings.HasPrefix(note, "Plan headroom: ") {
			t.Errorf("turn %d: note %q", i, note)
		}
	}
	if _, emit := DecideHeadroomNote(dir, "s2", steps[1].ws, noteNow, time.UTC); !emit {
		t.Error("a new session did not get the note on its first turn")
	}
	if _, emit := DecideHeadroomNote(dir, "s3", nil, noteNow, time.UTC); emit {
		t.Error("emitted a note with no window known")
	}
}

// The windows come from the status line's reading (with resets) for
// Claude, else from the daemon's figures for the provider.
func TestReadHeadroomWindows(t *testing.T) {
	limits := t.TempDir() + "/claude-limits.json"
	if err := claudelimits.Write(limits, claudelimits.Reading{
		ObservedAt: noteNow.Add(-time.Minute),
		FiveHour:   &claudelimits.Window{UsedPct: 62, ResetsAt: noteNow.Add(time.Hour).Unix()},
		SevenDay:   &claudelimits.Window{UsedPct: 39, ResetsAt: noteNow.Add(-time.Hour).Unix()}, // reset since
	}); err != nil {
		t.Fatal(err)
	}
	figs := boundaryledger.Dir(t.TempDir())
	if err := figs.PutFigures(boundaryledger.Figures{ObservedAt: noteNow.Add(-time.Minute), PlanLeftPct: map[string]map[string]float64{
		"openai": {"5h": 55, "week": 20},
	}}); err != nil {
		t.Fatal(err)
	}
	ws := ReadHeadroom(limits, figs, "anthropic", noteNow)
	if len(ws) != 1 || ws[0].Name != "5h" || ws[0].LeftPct != 38 || ws[0].ResetsAt.IsZero() {
		t.Errorf("anthropic windows = %+v, want the 5h window only (the week reset since)", ws)
	}
	ws = ReadHeadroom(limits, figs, "openai", noteNow)
	if len(ws) != 2 || ws[0].Name != "5h" || ws[0].LeftPct != 55 || ws[1].LeftPct != 20 {
		t.Errorf("openai windows = %+v, want both windows from the figures", ws)
	}
	if ws := ReadHeadroom(limits, figs, "cursor", noteNow); len(ws) != 0 {
		t.Errorf("windows for a provider with none = %+v", ws)
	}
}
