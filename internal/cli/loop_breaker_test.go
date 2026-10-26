package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

const loopBoundary = `boundaries:
  - name: loop-breaker
    action: continue
    when: { loop_calls: ">= 4" }
    require: ask
`

// secretCommand stands for whatever a tool's input holds: a path, a
// command, text from the prompt.
const secretCommand = "cat ~/private/plans-for-acme.txt # secret-marker-1234"

func loopHook(t *testing.T, tool string, input map[string]any) hookDecision {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": "/x/.claude/projects/-repo/s1.jsonl", "tool_use_id": "tu-1",
		"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input,
	})
	out, err := runCLI(t, body, "boundary-guard")
	if err != nil {
		t.Fatal(err)
	}
	var d hookDecision
	if out != "" {
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("hook output %q: %v", out, err)
		}
	}
	return d
}

// The loop breaker reads the tool's input only to hash it: across every
// file TokenOps writes, the input's text never appears, in shadow or
// enforced.
func TestLoopBreakerStoresNoArgumentText(t *testing.T) {
	for _, stage := range []string{"shadow", "enforce"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
			home := boundaryHome(t, loopBoundary+"    stage: "+stage+"\n", 0)
			var last hookDecision
			for i := range 5 {
				// The description changes on each retry; the command does not.
				last = loopHook(t, "Bash", map[string]any{"command": secretCommand, "description": "try " + string(rune('a'+i))})
			}
			if stage == "enforce" && last.HookSpecificOutput.PermissionDecision != "deny" {
				t.Fatalf("enforced loop breaker let the fifth repeat through: %+v", last)
			}
			if stage == "shadow" && last.HookSpecificOutput.PermissionDecision != "" {
				t.Fatalf("shadow loop breaker changed the call: %+v", last)
			}
			if envs := ledger(t, home); len(envs) == 0 {
				t.Fatal("nothing recorded")
			}
			err := filepath.WalkDir(filepath.Join(home, ".tokenops"), func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				b, err := os.ReadFile(path) //nolint:gosec // the test's own temp dir
				if err != nil {
					return err
				}
				for _, leak := range []string{"secret-marker-1234", "plans-for-acme", "try a"} {
					if strings.Contains(string(b), leak) {
						t.Errorf("%s holds the tool's input (%q)", path, leak)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Promotion is what changes behaviour: the same loop is let through in
// shadow and asked about once promoted.
func TestPromotingTheLoopBreakerMakesItAsk(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	boundaryHome(t, loopBoundary, 0)
	for range 4 {
		if d := loopHook(t, "Read", map[string]any{"file_path": "/repo/a.go"}); d.HookSpecificOutput.PermissionDecision != "" {
			t.Fatalf("shadow: %+v", d)
		}
	}
	if _, err := runCLI(t, nil, "boundaries", "promote", config.LoopBreakerName); err != nil {
		t.Fatal(err)
	}
	d := loopHook(t, "Read", map[string]any{"file_path": "/repo/a.go"})
	h := d.HookSpecificOutput
	if h.PermissionDecision != "ask" || !strings.Contains(h.PermissionDecisionReason, "the same Read call 5 times in a row") ||
		!strings.Contains(h.PermissionDecisionReason, "change approach") {
		t.Fatalf("promoted: %+v", h)
	}
	// An edit is progress: the window starts again.
	loopHook(t, "Edit", map[string]any{"file_path": "/repo/a.go", "old_string": "x", "new_string": "y"})
	if d := loopHook(t, "Read", map[string]any{"file_path": "/repo/a.go"}); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("after an edit: %+v", d)
	}
}

func TestBoundariesLoopBreakerAddsItOnRequest(t *testing.T) {
	home := boundaryHome(t, "listen: 127.0.0.1:7878\n", 0)
	_ = home
	out, err := runCLI(t, nil, "boundaries", "loop-breaker")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "loop-breaker is set, in shadow") {
		t.Fatalf("output:\n%s", out)
	}
	status, err := runCLI(t, nil, "boundaries", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, `"name": "loop-breaker"`) || !strings.Contains(status, `"stage": "shadow"`) {
		t.Fatalf("status:\n%s", status)
	}
}
