package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func runAgentHook(t *testing.T, stateDir, session, toolUseID string, toolInput map[string]any, transcript string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"session_id": session, "transcript_path": transcript, "tool_use_id": toolUseID,
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

// appendToolResult writes the transcript line Claude Code records when a
// tool call finishes: rejected is the operator answering No at the prompt.
func appendToolResult(t *testing.T, transcript, toolUseID string, rejected bool) {
	t.Helper()
	content, isErr := "found it", false
	if rejected {
		content, isErr = "The user doesn't want to proceed with this tool use. The tool use was rejected.", true
	}
	line, _ := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{"role": "user", "content": []map[string]any{
			{"type": "tool_result", "tool_use_id": toolUseID, "is_error": isErr, "content": content},
		}},
	})
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

var lookupCall = map[string]any{"description": "find config", "prompt": "find where the retention config is defined"}

// models: ask in an attended session proposes the move through the
// permission prompt, carrying the rewritten call.
func TestAskProposesTheMoveWhenAttended(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	writeCoachConfig(t, models+"coach:\n  autonomy: ask\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	start := ledgerLen(t)
	out := runAgentHook(t, dir, "ask-1", "toolu_1", lookupCall, tp)
	var got subagentHookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("no hook output: %q", out)
	}
	h := got.HookSpecificOutput
	if h.PermissionDecision != "ask" || h.UpdatedInput["model"] != "haiku" || h.UpdatedInput["description"] != "find config" {
		t.Fatalf("decision %q input %v; want ask with the rewritten call", h.PermissionDecision, h.UpdatedInput)
	}
	if r := h.PermissionDecisionReason; !strings.Contains(r, "claude-haiku-4-5 instead of claude-opus-5") {
		t.Errorf("the prompt's reason does not name the proposal: %q", r)
	}
	if got.SystemMessage != "" {
		t.Errorf("normal verbosity added a message that only shows after the answer: %q", got.SystemMessage)
	}
	if l := ledgerSince(t, start); len(l) != 1 || l[0]["channel"] != "approval" || l[0]["kind"] != "lookup" {
		t.Fatalf("ledger gained %v; want one lookup approval offer", l)
	}
}

// Nobody attending: an ask would be a denial the agent retries into, so
// the call is left alone.
func TestAskIsSilentWhenUnattended(t *testing.T) {
	writeCoachConfig(t, models+"coach:\n  autonomy: ask\n")
	for _, v := range []string{"0", ""} {
		t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", v)
		dir := t.TempDir()
		tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
		if out := runAgentHook(t, dir, "ask-2", "toolu_2", lookupCall, tp); out != "" {
			t.Fatalf("ATTENDED=%q asked: %s", v, out)
		}
	}
}

// Declining interrupts the turn; when the agent retries the same call it
// runs as planned, and the decline is recorded.
func TestADeclinedMoveRunsAsPlannedOnRetry(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	writeCoachConfig(t, models+"coach:\n  autonomy: ask\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	start := ledgerLen(t)
	if out := runAgentHook(t, dir, "ask-3", "toolu_3", lookupCall, tp); out == "" {
		t.Fatal("no proposal")
	}
	appendToolResult(t, tp, "toolu_3", true)
	if out := runAgentHook(t, dir, "ask-3", "toolu_4", lookupCall, tp); out != "" {
		t.Fatalf("asked again after a decline: %s", out)
	}
	l := ledgerSince(t, start)
	if len(l) != 2 || l[1]["type"] != "resolve" || l[1]["outcome"] != "ignored" || l[1]["id"] != l[0]["id"] {
		t.Fatalf("ledger = %v; want the proposal resolved as declined", l)
	}
	other := map[string]any{"description": "list files", "prompt": "list the files in the docs directory"}
	if out := runAgentHook(t, dir, "ask-3", "toolu_5", other, tp); out == "" {
		t.Error("a different call was not proposed after one decline")
	}
}

// An approved proposal is recorded as followed once its result is in the
// transcript.
func TestAnApprovedMoveIsRecorded(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	writeCoachConfig(t, models+"coach:\n  autonomy: ask\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	start := ledgerLen(t)
	runAgentHook(t, dir, "ask-4", "toolu_6", lookupCall, tp)
	appendToolResult(t, tp, "toolu_6", false)
	runAgentHook(t, dir, "ask-4", "toolu_7", map[string]any{"description": "review the design", "prompt": "refactor the router across every provider"}, tp)
	var resolved []map[string]any
	for _, e := range ledgerSince(t, start) {
		if e["type"] == "resolve" {
			resolved = append(resolved, e)
		}
	}
	if len(resolved) != 1 || resolved[0]["outcome"] != "followed" || resolved[0]["evidence"] != "approved at the prompt" {
		t.Fatalf("resolutions = %v; want one approval", resolved)
	}
}

// Declining interrupts the turn, and the operator's next prompt is the
// first hook to run: it settles the proposal.
func TestTheNextPromptSettlesADecline(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	writeCoachConfig(t, models+"coach:\n  autonomy: ask\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	start := ledgerLen(t)
	runAgentHook(t, dir, "s1", "toolu_8", lookupCall, tp)
	appendToolResult(t, tp, "toolu_8", true)
	runPromptHook(t, dir, "run it on the model you planned", tp)
	var outcomes []any
	for _, e := range ledgerSince(t, start) {
		if e["type"] == "resolve" && e["evidence"] == "declined at the prompt" {
			outcomes = append(outcomes, e["outcome"])
		}
	}
	if len(outcomes) != 1 || outcomes[0] != "ignored" {
		t.Fatalf("declines recorded = %v; want one", outcomes)
	}
}
