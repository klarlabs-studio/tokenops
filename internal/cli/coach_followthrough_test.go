package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func runPromptHook(t *testing.T, stateDir, prompt, transcript string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": transcript,
		"hook_event_name": "UserPromptSubmit", "prompt": prompt,
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
	return out.String()
}

// ledgerSince returns the ledger entries appended after the first n.
func ledgerSince(t *testing.T, n int) []map[string]any {
	t.Helper()
	entries, err := coachLedger().Load()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(entries)-n)
	for _, e := range entries[n:] {
		b, _ := json.Marshal(e)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		out = append(out, m)
	}
	return out
}

func ledgerLen(t *testing.T) int {
	t.Helper()
	entries, err := coachLedger().Load()
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// Advice the route guard gives is recorded, and the agent handing the work
// to a cheaper subagent afterwards is recorded as following it.
func TestAdviceAndItsFollowThroughAreRecorded(t *testing.T) {
	writeCoachConfig(t, models+"coach:\n  autonomy: advise\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	start := ledgerLen(t)
	if out := runPromptHook(t, dir, "show me the retention config", tp); out == "" {
		t.Fatal("no advice given")
	}
	got := ledgerSince(t, start)
	if len(got) != 1 || got[0]["type"] != "offer" || got[0]["channel"] != "advice" || got[0]["kind"] != "lookup" {
		t.Fatalf("after advice, ledger gained %v; want one lookup advice offer", got)
	}
	id := got[0]["id"]
	if out := runSubagentHook(t, dir, map[string]any{"description": "find config", "model": "haiku"}, tp); out != "" {
		t.Fatalf("advise rewrote a subagent: %s", out)
	}
	got = ledgerSince(t, start)
	if len(got) != 2 || got[1]["type"] != "resolve" || got[1]["id"] != id || got[1]["outcome"] != "followed" {
		t.Fatalf("after the cheaper subagent, ledger = %v; want the advice followed", got)
	}
}

// Lowering a power through the coach command is recorded, so moves made
// just before it count as undone.
func TestLoweringAPowerIsRecorded(t *testing.T) {
	writeCoachConfig(t, "coach:\n  autonomy: autonomous\n")
	start := ledgerLen(t)
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"coach", "set", "models", "advise"})
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := ledgerSince(t, start)
	if len(got) != 1 || got[0]["type"] != "lowered" || got[0]["power"] != config.PowerModels ||
		got[0]["from"] != "autonomous" || got[0]["to"] != "advise" {
		t.Fatalf("ledger gained %v; want models lowered from autonomous to advise", got)
	}
	start = ledgerLen(t)
	root = NewRoot()
	root.SetArgs([]string{"coach", "set", "models", "autonomous"})
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := ledgerSince(t, start); len(got) != 0 {
		t.Errorf("raising a power was recorded as lowering: %v", got)
	}
}
