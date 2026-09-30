package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
)

func writeCoachConfig(t *testing.T, body string) {
	t.Helper()
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

func runSubagentHook(t *testing.T, stateDir string, toolInput map[string]any, transcript string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": transcript,
		"hook_event_name": "PreToolUse", "tool_name": "Agent", "tool_input": toolInput,
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

const models = `optimizer:
  smart_routing:
    enabled: true
    models:
      anthropic: [claude-haiku-4-5, claude-sonnet-5, claude-opus-5]
`

// models: autonomous moves a lookup subagent off a flagship session, keeps
// every other input field, and records the move.
func TestSubagentGuardMovesWorkWhenAutonomous(t *testing.T) {
	writeCoachConfig(t, models+"coach:\n  autonomy: advise\n  powers:\n    models: autonomous\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	moves := coachcap.CountMoves(coachLedger(), config.PowerModels)
	out := runSubagentHook(t, dir, map[string]any{
		"description": "find config", "prompt": "find where the retention config is defined", "run_in_background": false,
	}, tp)
	var got subagentHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q (%v)", out, err)
	}
	h := got.HookSpecificOutput
	if h.PermissionDecision != "allow" || h.UpdatedInput["model"] != "haiku" {
		t.Fatalf("decision %q, model %v", h.PermissionDecision, h.UpdatedInput["model"])
	}
	if h.UpdatedInput["description"] != "find config" || h.UpdatedInput["prompt"] == nil {
		t.Errorf("other input fields dropped: %v", h.UpdatedInput)
	}
	if !strings.Contains(got.SystemMessage, "moved a subagent") {
		t.Errorf("normal verbosity says nothing: %q", got.SystemMessage)
	}
	if coachcap.CountMoves(coachLedger(), config.PowerModels) != moves+1 {
		t.Errorf("move not recorded in the follow-through ledger")
	}
}

// Below autonomous the subagent call is left alone.
func TestSubagentGuardLeavesCallsAloneBelowAutonomous(t *testing.T) {
	writeCoachConfig(t, models+"coach:\n  autonomy: advise\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	if out := runSubagentHook(t, dir, map[string]any{"prompt": "find where the retention config is defined"}, tp); out != "" {
		t.Fatalf("advise rewrote a subagent: %s", out)
	}
}

func TestSubagentGuardQuietSaysNothing(t *testing.T) {
	writeCoachConfig(t, models+"coach:\n  autonomy: autonomous\n  verbosity: quiet\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	out := runSubagentHook(t, dir, map[string]any{"prompt": "find where the retention config is defined"}, tp)
	var got subagentHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q", out)
	}
	if got.SystemMessage != "" || got.HookSpecificOutput.UpdatedInput["model"] != "haiku" {
		t.Errorf("quiet: message %q model %v", got.SystemMessage, got.HookSpecificOutput.UpdatedInput["model"])
	}
}

// The model policy holds with the coach off: a subagent inheriting a
// forbidden session model runs on the closest permitted one.
func TestSubagentGuardEnforcesModelPolicyWithTheCoachOff(t *testing.T) {
	writeCoachConfig(t, models+"model_policy:\n  deny: [\"*opus*\"]\ncoach:\n  autonomy: \"off\"\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	out := runSubagentHook(t, dir, map[string]any{"description": "design the storage layer", "prompt": "design it"}, tp)
	var got subagentHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q (%v)", out, err)
	}
	h := got.HookSpecificOutput
	if h.PermissionDecision != "allow" || h.UpdatedInput["model"] != "sonnet" {
		t.Fatalf("decision %q, model %v (%s)", h.PermissionDecision, h.UpdatedInput["model"], h.PermissionDecisionReason)
	}
	if !strings.Contains(got.SystemMessage, "model policy") {
		t.Errorf("the move is not explained: %q", got.SystemMessage)
	}
}

// With nothing permitted the Agent can name, the call is refused with the
// reason, so the agent can choose again.
func TestSubagentGuardRefusesWhenNothingIsPermitted(t *testing.T) {
	writeCoachConfig(t, models+"model_policy:\n  allow: [\"openai/*\"]\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	out := runSubagentHook(t, dir, map[string]any{"prompt": "anything", "model": "opus"}, tp)
	var got subagentHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q (%v)", out, err)
	}
	h := got.HookSpecificOutput
	if h.PermissionDecision != "deny" || !strings.Contains(h.PermissionDecisionReason, "model policy") {
		t.Fatalf("decision %q reason %q", h.PermissionDecision, h.PermissionDecisionReason)
	}
}

// install wires the Agent hook for Claude Code only.
func TestInstallAddsTheSubagentHookForClaudeCodeOnly(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	if _, err := runHooksCapturing(t, "install", "--route-guard", "--settings", settings); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), `"Agent"`) {
		t.Errorf("claude-code install has no Agent hook:\n%s", b)
	}
	codex := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := runHooksCapturing(t, "install", "--route-guard", "--client", "codex", "--settings", codex); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(codex); strings.Contains(string(b), `"Agent"`) {
		t.Errorf("codex install gained an Agent hook:\n%s", b)
	}
}
