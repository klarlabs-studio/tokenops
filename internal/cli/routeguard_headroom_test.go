package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
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
	writeCoachConfig(t, "plans:\n  anthropic: claude-max-20x\n")
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
	if !strings.HasPrefix(h.AdditionalContext, "Plan headroom: 5h window 38% left (resets ") || !strings.Contains(h.AdditionalContext, "week 61% left") {
		t.Errorf("context = %q, want the 5h and week windows", h.AdditionalContext)
	}
	if again := runSessionPromptHook(t, dir, "hn-1"); again != "" {
		t.Errorf("the note repeated on the next turn with nothing crossed: %s", again)
	}
}

// coach.headroom_note: false turns the note off.
func TestRouteGuardHeadroomNoteToggle(t *testing.T) {
	writeCoachConfig(t, "plans:\n  anthropic: claude-max-20x\ncoach:\n  headroom_note: false\n")
	writeLimits(t)
	if out := runSessionPromptHook(t, t.TempDir(), "hn-off"); out != "" {
		t.Errorf("note emitted while turned off: %s", out)
	}
	// So does turning the inform power off.
	writeCoachConfig(t, "plans:\n  anthropic: claude-max-20x\ncoach:\n  powers:\n    inform: \"off\"\n")
	if out := runSessionPromptHook(t, t.TempDir(), "hn-inform-off"); out != "" {
		t.Errorf("note emitted with the inform power off: %s", out)
	}
}

// `tokenops coach headroom-note off` writes the setting.
func TestCoachHeadroomNoteCommand(t *testing.T) {
	writeCoachConfig(t, "plans:\n  anthropic: claude-max-20x\n")
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
	writeCoachConfig(t, "plans:\n  anthropic: claude-max-20x\n")
	writeLimits(t)
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	if out := runAgentHook(t, dir, "hn-agent", "toolu_1", lookupCall, tp); strings.Contains(out, "Plan headroom") {
		t.Errorf("the subagent hook carried the note: %s", out)
	}
}
