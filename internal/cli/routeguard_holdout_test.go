package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// A move the holdout draws is not made: the call runs as the agent asked,
// and the move it would have been is recorded as a holdout, with the
// chance it had, for the moves that are made to be judged against.
func TestSubagentGuardHoldsBackARandomShareOfMoves(t *testing.T) {
	writeCoachConfig(t, models+"coach:\n  autonomy: advise\n  powers:\n    models: autonomous\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	ref := ""
	for i := 0; ref == ""; i++ {
		if r := fmt.Sprintf("toolu_%d", i); learning.InMoveHoldout(r, 10) {
			ref = r
		}
	}
	body, _ := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": tp, "tool_use_id": ref,
		"hook_event_name": "PreToolUse", "tool_name": "Agent",
		"tool_input": map[string]any{"description": "find config", "prompt": "find where the retention config is defined"},
	})
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"route-guard", "--dir", dir})
	root.SetIn(bytes.NewReader(body))
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(out.String()); s != "" {
		t.Fatalf("a held move changed the call: %s", s)
	}
	entries, err := coachLedger().Load()
	if err != nil {
		t.Fatal(err)
	}
	var held *ft.Entry
	for i, e := range entries {
		if e.Type == ft.EntryHoldout && e.Ref == ref {
			held = &entries[i]
		}
	}
	if held == nil || held.From == "" || held.To == "" || !strings.Contains(held.Evidence, "10%") {
		t.Fatalf("holdout entry = %+v", held)
	}
}
