package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

// writePressedWeek leaves the status line's reading of a week like the
// maintainer's: 54% left with 18% of it gone, so it runs out days early.
func writePressedWeek(t *testing.T) {
	t.Helper()
	now := time.Now()
	r := claudelimits.Reading{
		ObservedAt: now.Add(-time.Minute),
		FiveHour:   &claudelimits.Window{UsedPct: 10, ResetsAt: now.Add(4 * time.Hour).Unix()},
		SevenDay:   &claudelimits.Window{UsedPct: 46, ResetsAt: now.Add(5*24*time.Hour + 18*time.Hour).Unix()},
	}
	if err := claudelimits.Write("", r); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(claudelimits.DefaultPath()) })
}

// fourTiers offers a model on every tier, so the edit tier (default) is
// Opus and the deep tier Fable, as on a Max plan offering Fable. With only
// three models Opus is the deepest and edits leave it without pressure.
const fourTiers = `optimizer:
  smart_routing:
    enabled: true
    models:
      anthropic: [claude-haiku-4-5, claude-sonnet-5, claude-opus-5, claude-fable-5]
`

var editCall = map[string]any{"description": "add parser test", "prompt": "add a test for the parser"}

// Under no pressure an edit subagent keeps the session's model: the work
// alone does not move it.
func TestSubagentGuardLeavesEditsAloneWithoutPressure(t *testing.T) {
	writeCoachConfig(t, fourTiers+"coach:\n  autonomy: advise\n  powers:\n    models: autonomous\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	if out := runSubagentHook(t, dir, editCall, tp); out != "" {
		t.Fatalf("edit moved without pressure: %s", out)
	}
}

// With the week running out early, autonomous moves the edit one tier
// down, says why, and records the pressure as the move's evidence.
func TestSubagentGuardMovesEditsUnderPressure(t *testing.T) {
	writeCoachConfig(t, fourTiers+"coach:\n  autonomy: advise\n  verbosity: verbose\n  powers:\n    models: autonomous\n")
	writePressedWeek(t)
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	start := ledgerLen(t)
	out := runSubagentHook(t, dir, editCall, tp)
	var got subagentHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q", out)
	}
	h := got.HookSpecificOutput
	if h.UpdatedInput["model"] != "sonnet" {
		t.Fatalf("model %v; want sonnet", h.UpdatedInput["model"])
	}
	if r := h.PermissionDecisionReason; !strings.Contains(r, "Claude weekly runs out ~") || !strings.Contains(r, "edit work moved from claude-opus-5 to claude-sonnet-5") {
		t.Errorf("reason does not say why: %q", r)
	}
	if !strings.Contains(got.SystemMessage, "Claude weekly runs out") {
		t.Errorf("verbose message does not say why: %q", got.SystemMessage)
	}
	l := ledgerSince(t, start)
	if len(l) != 1 || l[0]["channel"] != "move" || l[0]["kind"] != "edit" ||
		!strings.Contains(l[0]["evidence"].(string), "plan_pressure: Claude weekly") ||
		!strings.Contains(l[0]["evidence"].(string), "claude_code_statusline") {
		t.Fatalf("ledger gained %v; want one edit move with the pressure as evidence", l)
	}
}

// models: ask proposes the same pressed move.
func TestAskProposesPressedMoves(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	writeCoachConfig(t, fourTiers+"coach:\n  autonomy: ask\n")
	writePressedWeek(t)
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	start := ledgerLen(t)
	out := runAgentHook(t, dir, "ask-pressed", "toolu_p", editCall, tp)
	var got subagentHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q", out)
	}
	h := got.HookSpecificOutput
	if h.PermissionDecision != "ask" || h.UpdatedInput["model"] != "sonnet" {
		t.Fatalf("decision %q model %v", h.PermissionDecision, h.UpdatedInput["model"])
	}
	if !strings.Contains(h.PermissionDecisionReason, "Claude weekly runs out") {
		t.Errorf("the prompt does not say why: %q", h.PermissionDecisionReason)
	}
	if l := ledgerSince(t, start); len(l) != 1 || l[0]["channel"] != "approval" || !strings.Contains(l[0]["evidence"].(string), "plan_pressure") {
		t.Fatalf("ledger gained %v", l)
	}
}

// A stale reading is no pressure: the hook fails open to routing by the
// work alone.
func TestSubagentGuardIgnoresStalePressure(t *testing.T) {
	writeCoachConfig(t, fourTiers+"coach:\n  autonomy: advise\n  powers:\n    models: autonomous\n")
	now := time.Now()
	if err := claudelimits.Write("", claudelimits.Reading{
		ObservedAt: now.Add(-2 * time.Hour),
		SevenDay:   &claudelimits.Window{UsedPct: 95, ResetsAt: now.Add(3 * 24 * time.Hour).Unix()},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(claudelimits.DefaultPath()) })
	dir := t.TempDir()
	tp := writeCoachTranscript(t, filepath.Join(dir), 1, "claude-opus-5")
	if out := runSubagentHook(t, dir, editCall, tp); out != "" {
		t.Fatalf("stale reading moved an edit: %s", out)
	}
}
