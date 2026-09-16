package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runHooks executes a `hooks` subcommand against the real root.
func runHooks(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRoot()
	root.SetArgs(append([]string{"hooks"}, args...))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root.Execute()
}

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

	// The wired entry must carry the --budget flag (default 50), not the old
	// --threshold/--cooldown flags.
	args := coachHookArgs(t, hooks)
	if !contains(args, "--budget") || !contains(args, "50") {
		t.Fatalf("coach-hook args should be [coach-hook --budget 50], got %v", args)
	}
	if contains(args, "--threshold") || contains(args, "--cooldown") {
		t.Fatalf("coach-hook args must not carry the old flags, got %v", args)
	}
}

// coachHookArgs extracts the args slice of the wired coach-hook command entry.
func coachHookArgs(t *testing.T, hooks map[string]any) []string {
	t.Helper()
	groups, _ := hooks["Stop"].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hlist, _ := gm["hooks"].([]any)
		for _, h := range hlist {
			hm, _ := h.(map[string]any)
			if !isMarkerEntry(hm, "coach-hook") {
				continue
			}
			raw, _ := hm["args"].([]any)
			out := make([]string, 0, len(raw))
			for _, a := range raw {
				s, _ := a.(string)
				out = append(out, s)
			}
			return out
		}
	}
	t.Fatalf("no coach-hook entry found")
	return nil
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
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

// Codex keeps hooks in ~/.codex/hooks.json, in the same nested shape
// Claude Code uses — but as a single command string, not command + args.
// Assuming otherwise would run the bare binary with no subcommand: a hook
// that installs, reports success, and does nothing.
func TestHooksInstallCodexWritesASingleCommandString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := runHooks(t, "install", "--coach", "--client", "codex", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	groups := doc.Hooks["Stop"]
	if len(groups) == 0 || len(groups[0].Hooks) == 0 {
		t.Fatalf("no Stop hook written:\n%s", b)
	}
	entry := groups[0].Hooks[0]
	if _, hasArgs := entry["args"]; hasArgs {
		t.Error("entry carries a separate args array; Codex documents one command string")
	}
	cmdStr, _ := entry["command"].(string)
	if !strings.Contains(cmdStr, "coach-hook") {
		t.Errorf("command = %q, want the coach-hook subcommand in it", cmdStr)
	}
}

// Codex has no file-read tool, so read-guard has nothing to intervene in.
// Writing the hook anyway and reporting success is the defect this
// codebase keeps finding in other people's tools.
func TestHooksInstallRefusesReadGuardOnCodex(t *testing.T) {
	err := runHooks(t, "install", "--read-guard", "--client", "codex")
	if err == nil {
		t.Fatal("install succeeded; want a refusal")
	}
	if !strings.Contains(err.Error(), "no file-read tool") {
		t.Errorf("error = %q, want it to name why read-guard cannot work there", err)
	}
}

// An unknown client is rejected rather than silently treated as Claude
// Code, which would write the wrong file and report success.
func TestHooksInstallRejectsAnUnknownClient(t *testing.T) {
	if err := runHooks(t, "install", "--coach", "--client", "aider"); err == nil {
		t.Error("an unsupported client was accepted")
	}
}

// Cursor's schema is FLAT where Claude Code's and Codex's are nested: an
// event maps straight to entries carrying `command`, with no inner
// "hooks" array, and the document needs a top-level "version". Reusing
// the nested writer would produce a file Cursor parses without error and
// never acts on.
func TestHooksInstallCursorWritesTheFlatSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := runHooks(t, "install", "--coach", "--client", "cursor", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	var doc struct {
		Version int                         `json:"version"`
		Hooks   map[string][]map[string]any `json:"hooks"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Version != 1 {
		t.Errorf("version = %d, want 1", doc.Version)
	}
	// Lower-camel: Cursor's event is "stop", not "Stop".
	entries := doc.Hooks["stop"]
	if len(entries) == 0 {
		t.Fatalf("no stop hook written:\n%s", b)
	}
	if _, nested := entries[0]["hooks"]; nested {
		t.Error("entry has a nested hooks array; Cursor's schema is flat")
	}
	cmdStr, _ := entries[0]["command"].(string)
	if !strings.Contains(cmdStr, "coach-hook") {
		t.Errorf("command = %q, want the coach-hook subcommand", cmdStr)
	}
}

// Re-running install must not duplicate our entry or rewrite a file it
// need not touch — `make install-hooks` runs once per clone and often
// more.
func TestHooksInstallCursorIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	for range 3 {
		if err := runHooks(t, "install", "--coach", "--client", "cursor", "--settings", path); err != nil {
			t.Fatalf("install: %v", err)
		}
	}
	var doc struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if n := len(doc.Hooks["stop"]); n != 1 {
		t.Errorf("stop has %d entries after three installs, want 1", n)
	}
}

// Cursor's beforeReadFile is observe-only: only beforeShellExecution and
// beforeMCPExecution honour a permission decision. A read cannot be
// refused there, so installing read-guard would arm a guard that never
// guards.
func TestHooksInstallRefusesReadGuardOnCursor(t *testing.T) {
	err := runHooks(t, "install", "--read-guard", "--client", "cursor")
	if err == nil {
		t.Fatal("install succeeded; want a refusal")
	}
	if !strings.Contains(err.Error(), "observe-only") {
		t.Errorf("error = %q, want it to name why read-guard cannot work there", err)
	}
}

// opencode has no config file to merge into — its extension point is a
// JavaScript module — so the installer generates one. Generated rather
// than shipped: a published package would mean a second artifact, a
// second release path, and version skew against the binary it calls.
func TestHooksInstallOpencodeGeneratesThePlugin(t *testing.T) {
	dir := t.TempDir()
	if err := runHooks(t, "install", "--read-guard", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "tokenops-read-guard.ts"))
	if err != nil {
		t.Fatalf("plugin not written: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`"tool.execute.before"`,       // the hook opencode calls
		`input?.tool !== "read"`,      // only file reads
		`Bun.spawn`,                   // reaches the binary
		"read-guard",                  // …with the right subcommand
		"throw new Error(denyReason)", // blocks only on an explicit deny
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated plugin is missing %q", want)
		}
	}
	// The absolute path to THIS binary, so the plugin cannot drift onto a
	// different install.
	if !strings.Contains(src, selfExe()) {
		t.Error("plugin does not hardcode the path to the binary that wrote it")
	}
}

// The failure that would hurt most: a guard in front of every file read
// that stops reads working. The throw sits outside the try so a bug in
// the error handling cannot swallow a real refusal, and nothing else can
// escape.
func TestOpencodePluginFailsOpen(t *testing.T) {
	dir := t.TempDir()
	if err := runHooks(t, "install", "--read-guard", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "tokenops-read-guard.ts"))
	src := string(b)
	if !strings.Contains(src, "} catch {") {
		t.Error("no catch-all; an unexpected error would block a read")
	}
	if strings.Contains(src, "throw") && strings.Index(src, "} catch {") > strings.Index(src, "throw new Error(denyReason)") {
		t.Error("the deny throw is inside the try; a handler bug could swallow it")
	}
}

// read-guard treats the mere presence of offset or limit as a ranged read
// and always allows those. Sending a literal null would therefore disable
// the guard entirely while appearing to work.
func TestOpencodePluginOmitsRangeFieldsRatherThanNulling(t *testing.T) {
	dir := t.TempDir()
	if err := runHooks(t, "install", "--read-guard", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "tokenops-read-guard.ts"))
	src := string(b)
	if strings.Contains(src, "offset: output.args.offset ?? null") {
		t.Error("offset is sent as null; read-guard would treat every read as ranged and allow it")
	}
	if !strings.Contains(src, "!== undefined && output.args.offset !== null") {
		t.Error("offset is not guarded before being included")
	}
}

// Re-running must not rewrite an identical file: `make install-hooks`
// runs once per clone and often more.
func TestHooksInstallOpencodeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := runHooks(t, "install", "--read-guard", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tokenops-read-guard.ts")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := runHooks(t, "install", "--read-guard", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("an unchanged plugin was rewritten")
	}
}

// The coaching nudge needs per-turn token counts, which tokenops cannot
// yet read from opencode. Declining out loud beats installing a hook that
// measures nothing.
func TestHooksInstallOpencodeRefusesCoach(t *testing.T) {
	err := runHooks(t, "install", "--coach", "--client", "opencode", "--settings", t.TempDir())
	if err == nil {
		t.Fatal("install --coach succeeded for opencode; want a refusal")
	}
	if !strings.Contains(err.Error(), "read-guard only") {
		t.Errorf("error = %q, want it to name what opencode does support", err)
	}
}
