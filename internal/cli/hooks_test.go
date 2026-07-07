package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// countMarker returns how many entries with the given marker are wired.
func countMarker(hooks map[string]any, marker string) int {
	return len(findMarkerEntries(hooks, marker))
}

func TestHooksInstall_CreatesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	run := func() error {
		root := NewRoot()
		root.SetArgs([]string{"hooks", "install", "--coach", "--settings", path})
		root.SetOut(os.Stderr)
		return root.Execute()
	}
	if err := run(); err != nil {
		t.Fatalf("install 1: %v", err)
	}
	hooks := hooksMap(readJSON(t, path))
	if got := countMarker(hooks, "coach-hook"); got != 1 {
		t.Fatalf("want 1 coach-hook entry, got %d", got)
	}

	// Second install must not duplicate.
	if err := run(); err != nil {
		t.Fatalf("install 2: %v", err)
	}
	hooks = hooksMap(readJSON(t, path))
	if got := countMarker(hooks, "coach-hook"); got != 1 {
		t.Fatalf("idempotency: want 1 coach-hook entry after 2 installs, got %d", got)
	}
}

func TestHooksInstall_PreservesUnrelatedHooksAndKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Pre-existing settings with an unrelated top-level key and an unrelated
	// PreToolUse hook that tokenops must not clobber.
	seed := map[string]any{
		"permissions": map[string]any{"allow": []any{"Bash(ls)"}},
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/bin/other", "args": []any{"do-thing"}},
					},
				},
			},
		},
	}
	b, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	root := NewRoot()
	root.SetArgs([]string{"hooks", "install", "--coach", "--read-guard", "--settings", path})
	root.SetOut(os.Stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	got := readJSON(t, path)
	if _, ok := got["permissions"]; !ok {
		t.Fatalf("unrelated top-level key 'permissions' was dropped")
	}
	hooks := hooksMap(got)
	if countMarker(hooks, "coach-hook") != 1 {
		t.Fatalf("coach-hook not wired")
	}
	if countMarker(hooks, "read-guard") != 1 {
		t.Fatalf("read-guard not wired")
	}
	// The unrelated Bash hook must still be present.
	pre, _ := hooks["PreToolUse"].([]any)
	foundBash := false
	for _, g := range pre {
		gm := g.(map[string]any)
		if matcherOf(gm) == "Bash" {
			foundBash = true
		}
	}
	if !foundBash {
		t.Fatalf("unrelated Bash PreToolUse hook was clobbered")
	}
	// Backup was written.
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("expected backup file: %v", err)
	}
}

func TestHooksInstall_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	root := NewRoot()
	root.SetArgs([]string{"hooks", "install", "--coach", "--dry-run", "--settings", path})
	root.SetOut(os.Stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not create the settings file")
	}
}

func TestHooksUninstall_RemovesOnlyOurs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	install := NewRoot()
	install.SetArgs([]string{"hooks", "install", "--coach", "--read-guard", "--settings", path})
	install.SetOut(os.Stderr)
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	un := NewRoot()
	un.SetArgs([]string{"hooks", "uninstall", "--coach", "--settings", path})
	un.SetOut(os.Stderr)
	if err := un.Execute(); err != nil {
		t.Fatalf("uninstall coach: %v", err)
	}
	hooks := hooksMap(readJSON(t, path))
	if countMarker(hooks, "coach-hook") != 0 {
		t.Fatalf("coach-hook should be removed")
	}
	if countMarker(hooks, "read-guard") != 1 {
		t.Fatalf("read-guard should remain")
	}
}

func TestHooksStatus_Runs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	install := NewRoot()
	install.SetArgs([]string{"hooks", "install", "--coach", "--settings", path})
	install.SetOut(os.Stderr)
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}
	st := NewRoot()
	st.SetArgs([]string{"hooks", "status", "--settings", path})
	st.SetOut(os.Stderr)
	if err := st.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
}

func TestHooksInstall_MalformedSettingsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := NewRoot()
	root.SetArgs([]string{"hooks", "install", "--coach", "--settings", path})
	root.SetOut(os.Stderr)
	if err := root.Execute(); err == nil {
		t.Fatalf("expected an error refusing to overwrite malformed settings")
	}
}
