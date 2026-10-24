package routeguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestToolResultsReadsRejectionsInBothContentShapes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	body := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","is_error":true,"content":"The user doesn't want to proceed with this tool use. Stop."}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b","is_error":true,"content":[{"type":"text","text":"The user doesn't want to proceed with this tool use."}]}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"c","content":"ok"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"d","is_error":true,"content":"command failed"}]}}
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := toolResults(p)
	want := map[string]Outcome{"a": OutcomeDeclined, "b": OutcomeDeclined, "c": OutcomeRan, "d": OutcomeRan}
	for id, o := range want {
		if v, ok := got[id]; !ok || v.Outcome != o {
			t.Errorf("%s: outcome=%v seen=%v; want %v", id, v.Outcome, ok, o)
		}
	}
}

func TestSettleProposalsKeepsDeclinesAndDropsApprovals(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	body := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"no","is_error":true,"content":"The user doesn't want to proceed with this tool use."}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"yes","content":"done"}]}}
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	Propose(dir, "s", Proposal{ID: "p1", ToolUseID: "no", Call: "c1"})
	Propose(dir, "s", Proposal{ID: "p2", ToolUseID: "yes", Call: "c2"})
	Propose(dir, "s", Proposal{ID: "p3", ToolUseID: "later", Call: "c3"})
	got := SettleProposals(dir, "s", p)
	if len(got) != 2 || got[0].ID != "p1" || got[0].Approved || got[1].ID != "p2" || !got[1].Approved {
		t.Fatalf("settled = %+v", got)
	}
	if !WasDeclined(dir, "s", "c1") || WasDeclined(dir, "s", "c2") || WasDeclined(dir, "s", "c3") {
		t.Error("declined calls not remembered correctly")
	}
	if again := SettleProposals(dir, "s", p); len(again) != 0 {
		t.Errorf("settled twice: %+v", again)
	}
}

func TestToolVerdictTellsThePersonFromClaudeCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	body := `{"timestamp":"2026-10-20T10:00:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"ok","content":"done"}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"no","is_error":true,"content":"The user doesn't want to proceed with this tool use."}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"sub-no","is_error":true,"content":"Permission for this tool use was denied. The tool use was rejected."}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"auto","is_error":true,"content":"Permission for this action was denied by the Claude Code auto mode classifier. Reason: [x]."}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"check","is_error":true,"content":"Permission for this action has been denied. Reason: x"}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"rule","is_error":true,"content":[{"type":"text","text":"Permission to use Bash with command rm -rf x has been denied."}]}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"plugin","is_error":true,"content":"Permission to use Bash denied by plugin guard: no"}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"hook","is_error":true,"content":"PreToolUse:Bash hook error: blocked"}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"freehook","is_error":true,"content":"not on fridays"}]}}
{"message":{"content":[{"type":"tool_result","tool_use_id":"echo","content":"Permission to use Bash has been denied."}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	want := map[string]Verdict{
		"ok": {Outcome: OutcomeRan, At: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}, "no": {Outcome: OutcomeDeclined},
		"sub-no": {Outcome: OutcomeDeclined},
		"auto":   {Outcome: OutcomeVendorStopped, Stopper: StopperAutoMode}, "check": {Outcome: OutcomeVendorStopped, Stopper: StopperPermission},
		"rule": {Outcome: OutcomeVendorStopped, Stopper: StopperPermissionRule}, "plugin": {Outcome: OutcomeVendorStopped, Stopper: StopperPlugin},
		"hook": {Outcome: OutcomeVendorStopped, Stopper: StopperHook},
		// A hook's free-text reason reads like a call that failed, and
		// output that merely quotes the wording is not an error.
		"freehook": {Outcome: OutcomeRan}, "echo": {Outcome: OutcomeRan},
	}
	for id, w := range want {
		v, seen := ToolVerdict(path, id)
		if !seen || v.Outcome != w.Outcome || v.Stopper != w.Stopper || !v.At.Equal(w.At) {
			t.Errorf("%s: %+v seen=%v; want %+v", id, v, seen, w)
		}
	}
	if _, seen := ToolVerdict(path, "later"); seen {
		t.Error("a call with no result was seen")
	}
}

func TestSettleProposalsDropsACallClaudeCodeStopped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")
	body := `{"message":{"content":[{"type":"tool_result","tool_use_id":"stopped","is_error":true,"content":"Permission to use Agent has been denied."}]}}
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	Propose(dir, "s", Proposal{ID: "p1", ToolUseID: "stopped", Call: "c1"})
	if got := SettleProposals(dir, "s", p); len(got) != 0 {
		t.Fatalf("a refused call was scored as an answer: %+v", got)
	}
	if WasDeclined(dir, "s", "c1") {
		t.Error("a refused call was taken for the person's decline")
	}
	if len(loadState(dir, "s").Proposals) != 0 {
		t.Error("the refused proposal is still pending")
	}
}
