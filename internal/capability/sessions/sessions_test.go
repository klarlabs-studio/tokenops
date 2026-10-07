package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// now is fixed so every window in these tests is deterministic.
var now = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// isolateHome points every client default at an empty temp home. Every
// test here pins Root, but a default resolved by mistake must still read
// nothing of the operator's.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
}

// session is one Claude Code transcript: an instruction and the agent's
// reply, optionally editing a file.
type session struct {
	id     string
	at     time.Time
	prompt string
	edits  []string
}

func (s session) lines() []string {
	ts := func(d time.Duration) string { return s.at.Add(d).Format(time.RFC3339) }
	prompt := fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":%q,"cwd":"/w/repo","message":{"role":"user","content":%q}}`,
		ts(0), s.id, s.prompt)
	out := make([]string, 0, 3)
	out = append(out, prompt)
	tools := make([]string, 0, len(s.edits))
	for _, f := range s.edits {
		tools = append(tools, fmt.Sprintf(`{"type":"tool_use","name":"Edit","input":{"file_path":%q}}`, f))
	}
	out = append(out, fmt.Sprintf(
		`{"type":"assistant","timestamp":%q,"sessionId":%q,"message":{"role":"assistant","model":"claude-x","usage":{"input_tokens":1000},"content":[%s]}}`,
		ts(30*time.Second), s.id, strings.Join(tools, ",")))
	out = append(out, fmt.Sprintf(
		`{"type":"assistant","timestamp":%q,"sessionId":%q,"message":{"role":"assistant","model":"claude-x","usage":{"input_tokens":1200},"content":[{"type":"text","text":"done"}]}}`,
		ts(time.Minute), s.id))
	return out
}

// writeRoot lays sessions out as a Claude Code projects directory.
func writeRoot(t *testing.T, sessions ...session) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		body := strings.Join(s.lines(), "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, s.id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestWindowResolvesDays(t *testing.T) {
	tests := []struct {
		name      string
		w         Window
		wantDays  int
		wantLabel string
		wantSince time.Time
	}{
		{"omitted days is the seven-day default", Window{}, 7, "last 7d", now.AddDate(0, 0, -7)},
		{"positive days", Window{Days: 30}, 30, "last 30d", now.AddDate(0, 0, -30)},
		{"one day", Window{Days: 1}, 1, "last 1d", now.AddDate(0, 0, -1)},
		{"all overrides days", Window{Days: 3, All: true}, -1, "all history", time.Time{}},
		{"negative days still reads everything", Window{Days: -2}, -2, "all history", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.w.days(); got != tt.wantDays {
				t.Errorf("days() = %d, want %d", got, tt.wantDays)
			}
			opts, label := tt.w.options(now, true)
			if label != tt.wantLabel || !opts.Since.Equal(tt.wantSince) {
				t.Errorf("options = since %v label %q, want %v %q", opts.Since, label, tt.wantSince, tt.wantLabel)
			}
			if !opts.WithPromptText {
				t.Error("options dropped WithPromptText")
			}
		})
	}
	opts, _ := Window{Root: "/r"}.options(now, false)
	if opts.Root != "/r" || opts.WithPromptText {
		t.Errorf("options = %+v, want the root carried and no prompt text", opts)
	}
}

func TestReadWarnings(t *testing.T) {
	a, b := errors.New("cursor: schema changed"), errors.New("opencode: schema changed")
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{"no error, no warnings", nil, nil},
		{"one error", a, []string{"cursor: schema changed"}},
		{"one warning per joined source", errors.Join(a, b), []string{"cursor: schema changed", "opencode: schema changed"}},
		{"wrapped single error stays whole", fmt.Errorf("read: %w", a), []string{"read: cursor: schema changed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readWarnings(tt.err)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") || (tt.want == nil) != (got == nil) {
				t.Errorf("readWarnings = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// interruptedRecords is a window in which every instruction was stopped
// by the operator — the clearest case for a named recommendation.
func interruptedRecords(n int) []agentdx.Record {
	var out []agentdx.Record
	for i := 0; i < n; i++ {
		at := now.Add(-time.Duration(n-i) * time.Hour)
		sid := fmt.Sprintf("s%d", i)
		out = append(out,
			agentdx.Record{At: at, SessionID: sid, Kind: agentdx.KindPrompt},
			agentdx.Record{At: at.Add(time.Second), SessionID: sid, Kind: agentdx.KindAssistantTurn, InputTokens: 1000},
			agentdx.Record{At: at.Add(2 * time.Second), SessionID: sid, Kind: agentdx.KindInterrupt},
		)
	}
	return out
}

func TestDXFromRecords(t *testing.T) {
	t.Run("no instructions explains how to widen the window", func(t *testing.T) {
		dx := DXFromRecords(nil, nil, "last 7d")
		if dx.Window != "last 7d" || dx.Recommendation != nil || !strings.Contains(dx.Note, "no instructions in this window") {
			t.Errorf("dx = %+v", dx)
		}
		if dx.Warnings != nil {
			t.Errorf("warnings = %v, want none", dx.Warnings)
		}
	})
	t.Run("unreadable sources become warnings, not a failure", func(t *testing.T) {
		err := errors.Join(errors.New("cursor unreadable"), errors.New("opencode unreadable"))
		dx := DXFromRecords(interruptedRecords(6), err, "all history")
		if len(dx.Warnings) != 2 || dx.Metrics.Prompts != 6 {
			t.Errorf("dx = %+v, want metrics from the readable records and two warnings", dx)
		}
	})
	t.Run("a poor dimension names one change", func(t *testing.T) {
		recs := interruptedRecords(8)
		dx := DXFromRecords(recs, nil, "last 7d")
		want, ok := agentdx.Recommend(agentdx.ComputeByProvider(recs))
		if !ok {
			t.Fatalf("fixture does not grade poorly: %+v", dx.Grades)
		}
		if dx.Recommendation == nil || *dx.Recommendation != (Recommendation{Title: want.Title, Evidence: want.Evidence, Action: want.Action}) {
			t.Errorf("recommendation = %+v, want %+v", dx.Recommendation, want)
		}
		if dx.Note != "" {
			t.Errorf("note = %q alongside a recommendation", dx.Note)
		}
	})
}

func TestComputeDXReadsTheWindow(t *testing.T) {
	isolateHome(t)
	root := writeRoot(t,
		session{id: "recent-1", at: now.Add(-2 * time.Hour), prompt: "add the retry", edits: []string{"/w/repo/a.go"}},
		session{id: "recent-2", at: now.Add(-26 * time.Hour), prompt: "rename the field", edits: []string{"/w/repo/b.go"}},
		session{id: "old", at: now.AddDate(0, 0, -20), prompt: "ancient work"},
	)

	tests := []struct {
		name        string
		w           Window
		wantPrompts int
		wantWindow  string
	}{
		{"default seven days", Window{Root: root}, 2, "last 7d"},
		{"one day", Window{Root: root, Days: 1}, 1, "last 1d"},
		{"all history", Window{Root: root, All: true}, 3, "all history"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dx, _ := ComputeDXWithCurve(tt.w, now)
			if dx.Metrics.Prompts != tt.wantPrompts || dx.Window != tt.wantWindow {
				t.Errorf("prompts %d window %q, want %d %q", dx.Metrics.Prompts, dx.Window, tt.wantPrompts, tt.wantWindow)
			}
			if dx.Warnings != nil {
				t.Errorf("warnings = %v", dx.Warnings)
			}
			if dx.Recommendation == nil && dx.Note == "" {
				t.Error("neither a recommendation nor a note: the caller is left with nothing to say")
			}
			if plain := ComputeDX(tt.w, now); plain.Metrics.Prompts != dx.Metrics.Prompts {
				t.Errorf("ComputeDX prompts %d != ComputeDXWithCurve %d", plain.Metrics.Prompts, dx.Metrics.Prompts)
			}
		})
	}

	t.Run("an empty root says so", func(t *testing.T) {
		dx := ComputeDX(Window{Root: t.TempDir()}, now)
		if dx.Metrics.Prompts != 0 || !strings.Contains(dx.Note, "no instructions") {
			t.Errorf("dx = %+v", dx)
		}
	})
}

func storyRoot(t *testing.T, n int) string {
	t.Helper()
	var ss []session
	for i := 0; i < n; i++ {
		ss = append(ss, session{
			id:     fmt.Sprintf("s%02d", i),
			at:     now.Add(-time.Duration(n-i) * time.Hour), // s00 oldest
			prompt: fmt.Sprintf("task number %02d", i),
			edits:  []string{fmt.Sprintf("/w/repo/f%02d.go", i)},
		})
	}
	return writeRoot(t, ss...)
}

func TestComputeStoryLimitsNewestFirst(t *testing.T) {
	isolateHome(t)
	root := storyRoot(t, 12)
	tests := []struct {
		name      string
		limit     int
		wantTasks int
	}{
		{"zero limit means ten", 0, 10},
		{"positive limit truncates", 3, 3},
		{"limit above the count returns all", 50, 12},
		{"negative limit returns all", -1, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := ComputeStory(Window{Root: root}, tt.limit, true, now)
			if len(st.Tasks) != tt.wantTasks {
				t.Fatalf("tasks = %d, want %d", len(st.Tasks), tt.wantTasks)
			}
			if st.Tasks[0].SessionID != "s11" || st.Tasks[0].Title != "task number 11" {
				t.Errorf("first task = %+v, want the newest (s11)", st.Tasks[0])
			}
			for i := 1; i < len(st.Tasks); i++ {
				if st.Tasks[i].Start > st.Tasks[i-1].Start {
					t.Errorf("task %d starts after task %d: not newest first", i, i-1)
				}
			}
			if st.Note != "" || st.TitlesWithheld {
				t.Errorf("story = note %q withheld %v", st.Note, st.TitlesWithheld)
			}
		})
	}
}

func TestComputeStoryTaskFields(t *testing.T) {
	isolateHome(t)
	start := now.Add(-3 * time.Hour)
	root := writeRoot(t, session{id: "one", at: start, prompt: "wire the probe", edits: []string{"/w/repo/probe.go"}})
	st := ComputeStory(Window{Root: root}, 0, true, now)
	if len(st.Tasks) != 1 {
		t.Fatalf("tasks = %+v", st.Tasks)
	}
	task := st.Tasks[0]
	if task.SessionID != "one" || task.Title != "wire the probe" || task.Instructions != 1 {
		t.Errorf("task = %+v", task)
	}
	if task.Start != start.Format(time.RFC3339) || task.End < task.Start || task.DurationSec < 0 {
		t.Errorf("timing = %s..%s (%vs)", task.Start, task.End, task.DurationSec)
	}
	if task.Turns < 1 || task.ToolCalls != 1 || task.PeakContext != 1200 {
		t.Errorf("turns %d tools %d peak %d, want ≥1, 1, 1200", task.Turns, task.ToolCalls, task.PeakContext)
	}
	if len(task.Files) != 1 || task.Files[0] != "/w/repo/probe.go" {
		t.Errorf("files = %v", task.Files)
	}
	if task.Frictions == nil {
		t.Error("frictions is nil; the API contract serves an empty list")
	}
	if task.Boundary == "" {
		t.Error("boundary is empty")
	}
	if !task.Clean || len(task.Frictions) != 0 {
		t.Errorf("clean %v frictions %v, want a clean task", task.Clean, task.Frictions)
	}
}

// Friction is reported in words the operator can act on, and a task
// with any is not clean.
func TestComputeStoryReportsFriction(t *testing.T) {
	isolateHome(t)
	root := writeRoot(t, session{id: "rw", at: now.Add(-time.Hour), prompt: "fix the parser",
		edits: []string{"/w/repo/parse.go", "/w/repo/lex.go", "/w/repo/parse.go"}})
	st := ComputeStory(Window{Root: root}, 0, true, now)
	if len(st.Tasks) != 1 {
		t.Fatalf("tasks = %+v", st.Tasks)
	}
	task := st.Tasks[0]
	if task.Clean || len(task.Frictions) != 1 || !strings.Contains(task.Frictions[0], "edited the same file twice") {
		t.Errorf("clean %v frictions %q, want one rework friction", task.Clean, task.Frictions)
	}
}

// The daemon API serves derived figures, never prompt text (ADR 0010 §5).
func TestComputeStoryWithheldTitles(t *testing.T) {
	isolateHome(t)
	root := storyRoot(t, 3)
	st := ComputeStory(Window{Root: root}, 0, false, now)
	if !st.TitlesWithheld || len(st.Tasks) != 3 {
		t.Fatalf("story = %+v", st)
	}
	for _, task := range st.Tasks {
		if task.Title != "" {
			t.Errorf("task %s title %q leaked with titles off", task.SessionID, task.Title)
		}
	}
}

func TestComputeStoryEmptyWindow(t *testing.T) {
	isolateHome(t)
	root := writeRoot(t, session{id: "old", at: now.AddDate(0, 0, -30), prompt: "long ago"})
	st := ComputeStory(Window{Root: root}, 0, true, now)
	if len(st.Tasks) != 0 || st.Tasks == nil || !strings.Contains(st.Note, "no tasks in this window") || st.Window != "last 7d" {
		t.Errorf("story = %+v, want an empty (non-nil) task list and a note", st)
	}
	if all := ComputeStory(Window{Root: root, All: true}, 0, true, now); len(all.Tasks) != 1 {
		t.Errorf("all history tasks = %d, want 1", len(all.Tasks))
	}
}

func TestPromptFindings(t *testing.T) {
	isolateHome(t)
	root := writeRoot(t,
		session{id: "a", at: now.Add(-time.Hour), prompt: "fix it"},
		session{id: "b", at: now.Add(-2 * time.Hour), prompt: "fix it"},
		session{id: "c", at: now.Add(-3 * time.Hour), prompt: "fix it"},
		session{id: "d", at: now.Add(-4 * time.Hour), prompt: "add a retry with exponential backoff to the vendor poller and cover it with a test"},
		session{id: "old", at: now.AddDate(0, 0, -10), prompt: "something from last month"},
	)

	t.Run("default window is the last seven days", func(t *testing.T) {
		f, err := PromptFindings(PromptWindow{Root: root}, true, now)
		if err != nil {
			t.Fatal(err)
		}
		if f.TotalPrompts != 4 {
			t.Errorf("total = %d, want 4 (the old prompt is outside the default window)", f.TotalPrompts)
		}
	})
	t.Run("explicit since and until bound the window", func(t *testing.T) {
		f, err := PromptFindings(PromptWindow{Root: root, Since: now.AddDate(0, 0, -30), Until: now.Add(-150 * time.Minute)}, true, now)
		if err != nil {
			t.Fatal(err)
		}
		if f.TotalPrompts != 3 {
			t.Errorf("total = %d, want 3 (c, d and the old one)", f.TotalPrompts)
		}
	})
	t.Run("session and limit narrow the scan", func(t *testing.T) {
		f, err := PromptFindings(PromptWindow{Root: root, SessionID: "d"}, true, now)
		if err != nil || f.TotalPrompts != 1 {
			t.Errorf("session d: total %d err %v, want 1", f.TotalPrompts, err)
		}
		f, err = PromptFindings(PromptWindow{Root: root, Limit: 2}, true, now)
		if err != nil || f.TotalPrompts != 2 {
			t.Errorf("limit 2: total %d err %v, want 2", f.TotalPrompts, err)
		}
	})
	t.Run("with text the repeat is quoted", func(t *testing.T) {
		f, err := PromptFindings(PromptWindow{Root: root}, true, now)
		if err != nil {
			t.Fatal(err)
		}
		if !quotes(f, "fix it") {
			t.Errorf("findings with text quote nothing of %q: %+v", "fix it", f)
		}
	})
	t.Run("without text nothing typed survives", func(t *testing.T) {
		f, err := PromptFindings(PromptWindow{Root: root}, false, now)
		if err != nil {
			t.Fatal(err)
		}
		if f.TotalPrompts != 4 {
			t.Errorf("total = %d, want the counts kept", f.TotalPrompts)
		}
		if quotes(f, "fix it") || quotes(f, "exponential backoff") {
			t.Errorf("findings without text still quote an instruction: %+v", f)
		}
	})
}

// quotes reports whether any quoted field of f carries text.
func quotes(f Findings, text string) bool {
	for _, s := range append(append([]string{}, f.VagueShortSamples...), f.RegenerateSamples...) {
		if strings.Contains(s, text) {
			return true
		}
	}
	for _, r := range f.RepeatedPrompts {
		if strings.Contains(r.Text, text) {
			return true
		}
	}
	for _, r := range f.Recommendations {
		for _, e := range r.Evidence {
			if strings.Contains(e, text) {
				return true
			}
		}
	}
	return false
}
