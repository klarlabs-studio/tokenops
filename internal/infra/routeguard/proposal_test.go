package routeguard

import (
	"os"
	"path/filepath"
	"testing"
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
	want := map[string]bool{"a": true, "b": true, "c": false, "d": false}
	for id, rejected := range want {
		if r, ok := got[id]; !ok || r != rejected {
			t.Errorf("%s: rejected=%v seen=%v; want %v", id, r, ok, rejected)
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
