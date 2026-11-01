package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// isolateHome points every default path the hook writes (the coach's
// ledger, the savings ledger, the config) into temp dirs.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

type hookOut struct {
	HookSpecificOutput struct {
		PermissionDecision string `json:"permissionDecision"`
		AdditionalContext  string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func runGuard(t *testing.T, mode readguard.Mode, dir string, speak func(string) bool, payload map[string]any) (hookOut, string) {
	t.Helper()
	body, _ := json.Marshal(payload)
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetIn(bytes.NewReader(body))
	cmd.SetOut(&out)
	if err := runReadGuardHook(cmd, mode, dir, speak); err != nil {
		t.Fatal(err)
	}
	var h hookOut
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &h); err != nil {
			t.Fatalf("hook output is not JSON: %q", out.String())
		}
	}
	return h, out.String()
}

func readPayload(file, cwd string) map[string]any {
	return map[string]any{
		"session_id": "s1", "cwd": cwd, "tool_name": "Read",
		"tool_input": map[string]any{"file_path": file},
	}
}

func speakAlways(string) bool { return true }

// The third read of a file carries the note, and the guard's decision on
// every read is the one it makes with no tips at all.
func TestReadGuardHookAddsTheChurnNoteWithoutChangingTheDecision(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	file := filepath.Join(cwd, "internal", "main.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []readguard.Mode{readguard.ModeObserve, readguard.ModeActive} {
		t.Run(string(mode), func(t *testing.T) {
			withTips, without := t.TempDir(), t.TempDir()
			for i := range 4 {
				got, raw := runGuard(t, mode, withTips, speakAlways, readPayload(file, cwd))
				want, _ := runGuard(t, mode, without, nil, readPayload(file, cwd))
				if got.HookSpecificOutput.PermissionDecision != want.HookSpecificOutput.PermissionDecision {
					t.Fatalf("read %d: decision %q with tips, %q without", i+1,
						got.HookSpecificOutput.PermissionDecision, want.HookSpecificOutput.PermissionDecision)
				}
				note := got.HookSpecificOutput.AdditionalContext
				if i == 2 {
					if !strings.Contains(note, "main.go (internal/main.go)") || !strings.Contains(note, "3 times") {
						t.Fatalf("third read: note = %q", note)
					}
					if mode == readguard.ModeObserve && strings.Contains(raw, "permissionDecision") {
						t.Fatalf("a note-only output carries a permission decision: %s", raw)
					}
				} else if note != "" {
					t.Fatalf("read %d carried a note: %q", i+1, note)
				}
			}
		})
	}
	// Each tip is recorded for the coach's follow-through.
	b, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".tokenops", "coach", "followthrough.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"kind":"reread_churn"`); n != 2 || !strings.Contains(string(b), `"via":"agent"`) {
		t.Fatalf("ledger = %s", b)
	}
}

func TestReadGuardHookSearchLoop(t *testing.T) {
	isolateHome(t)
	grep := map[string]any{
		"session_id": "s1", "cwd": "/repo", "tool_name": "Grep",
		"tool_input": map[string]any{"pattern": "func Evaluate", "path": "/repo/internal"},
	}
	edit := map[string]any{
		"session_id": "s1", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": "/repo/internal/x.go", "old_string": "a", "new_string": "b"},
	}
	tests := []struct {
		name  string
		calls []map[string]any
		note  bool
	}{
		{"three searches", []map[string]any{grep, grep, grep}, true},
		{"an edit in between", []map[string]any{grep, grep, edit, grep}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var last hookOut
			var raw string
			for _, c := range tt.calls {
				last, raw = runGuard(t, readguard.ModeActive, dir, speakAlways, c)
			}
			note := last.HookSpecificOutput.AdditionalContext
			if tt.note != (note != "") {
				t.Fatalf("note = %q, want one: %v", note, tt.note)
			}
			if tt.note && !strings.Contains(note, `Grep for "func Evaluate" in internal`) {
				t.Fatalf("note does not name the search: %q", note)
			}
			if strings.Contains(raw, "permissionDecision") {
				t.Fatalf("a search was given a permission decision: %s", raw)
			}
		})
	}
}

// The gate follows the coach: off or quiet says nothing.
func TestLoopTipGateFollowsTheCoach(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   bool
	}{
		{"defaults", "", true},
		{"inform off", "coach:\n  powers:\n    inform: off\n", false},
		{"quiet", "coach:\n  verbosity: quiet\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			if tt.config != "" {
				writeCoachConfig(t, tt.config)
			}
			if got := loopTipGate(&rootFlags{})(readguard.TipSearchLoop); got != tt.want {
				t.Fatalf("gate = %v, want %v", got, tt.want)
			}
		})
	}
}

// An install from before the loop tips has read-guard on Read only;
// installing again adds the second matcher, and a third run changes
// nothing.
func TestHooksInstallUpgradesReadGuardToTheLoopMatcher(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	exe := selfExe()
	old := map[string]any{"hooks": map[string]any{"PreToolUse": []any{
		map[string]any{"matcher": "Read", "hooks": []any{commandEntry(exe, readGuardArgs(""))}},
	}}}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooksCapturing(t, "install", "--read-guard", "--settings", path); err != nil {
		t.Fatal(err)
	}
	hooks := hooksMap(readJSON(t, path))
	matchers := map[string]bool{}
	for _, loc := range findMarkerEntries(hooks, "read-guard") {
		matchers[loc.matcher] = true
	}
	if len(matchers) != 2 || !matchers["Read"] || !matchers[loopTipMatcher] {
		t.Fatalf("read-guard matchers after upgrade = %v", matchers)
	}
	out, err := runHooksCapturing(t, "install", "--read-guard", "--settings", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Already up to date") {
		t.Fatalf("a second install changed something:\n%s", out)
	}
}
