package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
)

// writeLimits leaves a fresh status line reading: 5h 62% used, week 39%.
func writeLimits(t *testing.T) {
	t.Helper()
	now := time.Now()
	r := claudelimits.Reading{
		ObservedAt: now.Add(-time.Minute),
		FiveHour:   &claudelimits.Window{UsedPct: 62, ResetsAt: now.Add(2 * time.Hour).Unix()},
		SevenDay:   &claudelimits.Window{UsedPct: 39, ResetsAt: now.Add(3 * 24 * time.Hour).Unix()},
	}
	if err := claudelimits.Write("", r); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(claudelimits.DefaultPath()) })
}

func runSessionPromptHook(t *testing.T, stateDir, session string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"session_id": session, "hook_event_name": "UserPromptSubmit", "prompt": "add a test for the parser",
	})
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"route-guard", "--dir", stateDir})
	root.SetIn(bytes.NewReader(body))
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}

// The headroom note reaches the agent through UserPromptSubmit's
// additionalContext, on its own: model routing being off does not hold
// it back, and it is said once, not on every turn.
func TestRouteGuardInjectsTheHeadroomNoteOncePerStep(t *testing.T) {
	writeCoachConfig(t, "plans:\n  anthropic: claude-ultra\n")
	writeLimits(t)
	dir := t.TempDir()
	out := runSessionPromptHook(t, dir, "hn-1")
	var got promptHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q", out)
	}
	h := got.HookSpecificOutput
	if h.HookEventName != "UserPromptSubmit" {
		t.Errorf("event = %q, want UserPromptSubmit", h.HookEventName)
	}
	// The fixture's 5h window runs out before it resets, so the note says when.
	if !strings.HasPrefix(h.AdditionalContext, "Plan headroom: 5h window 38% left (runs out ~") || !strings.Contains(h.AdditionalContext, "resets ") || !strings.Contains(h.AdditionalContext, "week 61% left") {
		t.Errorf("context = %q, want the 5h and week windows", h.AdditionalContext)
	}
	if again := runSessionPromptHook(t, dir, "hn-1"); again != "" {
		t.Errorf("the note repeated on the next turn with nothing crossed: %s", again)
	}
}

// coach.headroom_note: false turns the note off.
func TestRouteGuardHeadroomNoteToggle(t *testing.T) {
	writeCoachConfig(t, "plans:\n  anthropic: claude-ultra\ncoach:\n  headroom_note: false\n")
	writeLimits(t)
	if out := runSessionPromptHook(t, t.TempDir(), "hn-off"); out != "" {
		t.Errorf("note emitted while turned off: %s", out)
	}
	// So does turning the inform power off.
	writeCoachConfig(t, "plans:\n  anthropic: claude-ultra\ncoach:\n  powers:\n    inform: \"off\"\n")
	if out := runSessionPromptHook(t, t.TempDir(), "hn-inform-off"); out != "" {
		t.Errorf("note emitted with the inform power off: %s", out)
	}
}

// `tokenops coach headroom-note off` writes the setting.
func TestCoachHeadroomNoteCommand(t *testing.T) {
	writeCoachConfig(t, "plans:\n  anthropic: claude-ultra\n")
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"coach", "headroom-note", "off"})
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	path, _ := config.DefaultPath()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CoachHeadroomNote() {
		t.Errorf("headroom note still on after `coach headroom-note off`:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "headroom note: off") {
		t.Errorf("status does not show the setting:\n%s", out.String())
	}
}

// The note goes only where per-turn content belongs: no TokenOps hook is
// wired to SessionStart, whose output would sit in the session's prefix,
// and the subagent path never carries it.
func TestHeadroomNoteTravelsOnlyPerTurn(t *testing.T) {
	for _, s := range specsForModeWithRoute(true, true, true, true, 1, "", "anthropic") {
		if s.event == "SessionStart" {
			t.Errorf("hook %s is wired to SessionStart", s.name)
		}
	}
	writeCoachConfig(t, "plans:\n  anthropic: claude-ultra\n")
	writeLimits(t)
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	if out := runAgentHook(t, dir, "hn-agent", "toolu_1", lookupCall, tp); strings.Contains(out, "Plan headroom") {
		t.Errorf("the subagent hook carried the note: %s", out)
	}
}

// The note passes `tokenops coach lint-context`: the route-guard command
// as installed prints nothing volatile by its text, and the notes
// transcripts record (resets as clock times, different shares) carry no
// timestamp, date, id or counter the lint flags.
func TestHeadroomNotePassesThePrefixLint(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const command = "/usr/local/bin/tokenops route-guard --provider anthropic"
	settings, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}},
	}})
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	notes := []string{
		routeguard.HeadroomNote([]routeguard.NoteWindow{{Name: "5h", LeftPct: 38, ResetsAt: now.Add(140 * time.Minute)}, {Name: "week", LeftPct: 61, ResetsAt: now.Add(81 * time.Hour)}}, now, time.UTC),
		routeguard.HeadroomNote([]routeguard.NoteWindow{{Name: "5h", LeftPct: 9, ResetsAt: now.Add(40 * time.Minute)}}, now, time.UTC),
	}
	root := filepath.Join(home, ".claude", "projects")
	for i, note := range notes {
		if ms := prefixlint.ScanText(note); len(ms) != 0 {
			t.Errorf("lint flags %q: %+v", note, ms)
		}
		out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "UserPromptSubmit", "additionalContext": note}})
		rec, _ := json.Marshal(map[string]any{"type": "attachment", "sessionId": "s", "attachment": map[string]any{
			"type": "hook_success", "hookName": "UserPromptSubmit", "hookEvent": "UserPromptSubmit",
			"command": command, "stdout": string(out), "content": string(out), "exitCode": 0,
		}})
		p := filepath.Join(root, "proj", "s"+itoa(int64(i))+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(rec, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := prefixlint.Lint(prefixlint.Sources{Home: home, ClaudeDir: claudeDir, TranscriptRoot: root})
	if r.Transcripts != len(notes) {
		t.Errorf("transcripts read = %d, want %d", r.Transcripts, len(notes))
	}
	for _, f := range r.Findings {
		t.Errorf("lint finding on the headroom note: %+v", f)
	}
}
