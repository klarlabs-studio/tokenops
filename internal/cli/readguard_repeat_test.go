package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

func bashPayload(command, cwd string) map[string]any {
	return map[string]any{
		"session_id": "s1", "cwd": cwd, "tool_name": "Bash",
		"tool_input": map[string]any{"command": command},
	}
}

// In active mode a read-only command run again with nothing changed is
// refused with a pointer to the earlier run; a command that changes
// things in between lets it run. Observe mode never refuses, and both
// record the repeat.
func TestReadGuardAnswersARepeatedCommand(t *testing.T) {
	isolateHome(t)
	cwd := t.TempDir()
	dir := t.TempDir()
	if got, raw := runGuard(t, readguard.ModeActive, dir, nil, bashPayload("git status", cwd)); raw != "" {
		t.Fatalf("first run answered: %+v", got)
	}
	got, _ := runGuard(t, readguard.ModeActive, dir, nil, bashPayload("git status", cwd))
	if got.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("repeat not refused: %+v", got)
	}
	runGuard(t, readguard.ModeActive, dir, nil, bashPayload("go generate ./...", cwd))
	if got, raw := runGuard(t, readguard.ModeActive, dir, nil, bashPayload("git status", cwd)); raw != "" {
		t.Fatalf("refused after a changing command: %+v", got)
	}

	obs := t.TempDir()
	for range 2 {
		if got, raw := runGuard(t, readguard.ModeObserve, obs, nil, bashPayload("git status", cwd)); raw != "" {
			t.Fatalf("observe mode answered: %+v", got)
		}
	}
	for d, want := range map[string][2]int{dir: {1, 1}, obs: {1, 0}} {
		s, err := readguard.ReadStats(d)
		if err != nil {
			t.Fatal(err)
		}
		if s.RepeatCommands != want[0] || s.RepeatCommandsBlocked != want[1] {
			t.Errorf("stats = %+v, want %v", s, want)
		}
	}
}

// An install over the matcher read-guard used before Bash moves it to the
// new one rather than leaving both, which would run it twice for a Grep.
func TestHooksInstallMovesReadGuardOffTheOldLoopMatcher(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	exe := selfExe()
	old := map[string]any{"hooks": map[string]any{"PreToolUse": []any{
		map[string]any{"matcher": "Read", "hooks": []any{commandEntry(exe, readGuardArgs(""))}},
		map[string]any{"matcher": legacyLoopTipMatcher, "hooks": []any{
			commandEntry(exe, readGuardArgs("")),
			map[string]any{"type": "command", "command": "other-tool"},
		}},
	}}}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runHooksCapturing(t, "install", "--read-guard", "--settings", path); err != nil {
		t.Fatal(err)
	}
	hooks := hooksMap(readJSON(t, path))
	matchers := map[string]int{}
	for _, loc := range findMarkerEntries(hooks, "read-guard") {
		matchers[loc.matcher]++
	}
	if len(matchers) != 2 || matchers["Read"] != 1 || matchers[loopTipMatcher] != 1 {
		t.Fatalf("read-guard matchers = %v", matchers)
	}
	raw, _ := json.Marshal(hooks)
	if !strings.Contains(string(raw), "other-tool") {
		t.Errorf("another tool's entry under the old matcher was removed: %s", raw)
	}
	out, err := runHooksCapturing(t, "install", "--read-guard", "--settings", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Already up to date") {
		t.Fatalf("a second install changed something:\n%s", out)
	}
}
