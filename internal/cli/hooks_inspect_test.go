package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runHooksCapturing executes a `hooks` subcommand and returns what it printed,
// because what status *says* is the whole behaviour under test here.
func runHooksCapturing(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := NewRoot()
	root.SetArgs(append([]string{"hooks"}, args...))
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	return buf.String(), err
}

// A guard that install can wire but status cannot see is worse than one that
// was never wired: the operator is told nothing is there while it runs on
// every turn. route-guard shipped in exactly that state.
func TestHooksStatusReportsRouteGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := runHooksCapturing(t, "install", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := runHooksCapturing(t, "status", "--settings", path)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "route-guard") {
		t.Fatalf("status did not report the installed route-guard:\n%s", out)
	}
	if !strings.Contains(out, "UserPromptSubmit") {
		t.Fatalf("status did not report the route-guard event:\n%s", out)
	}
}

// The tool that installs a hook must be able to remove it. Anything else
// leaves the operator editing settings.json by hand.
func TestHooksUninstallRemovesRouteGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := runHooksCapturing(t,
		"install", "--coach", "--read-guard", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := runHooksCapturing(t, "uninstall", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	hooks := hooksMap(readJSON(t, path))
	if got := countMarker(hooks, "route-guard"); got != 0 {
		t.Fatalf("route-guard should be gone, found %d", got)
	}
	if got := countMarker(hooks, "coach-hook"); got != 1 {
		t.Fatalf("coach-hook should remain, found %d", got)
	}
	if got := countMarker(hooks, "read-guard"); got != 1 {
		t.Fatalf("read-guard should remain, found %d", got)
	}
}

// An uninstall naming nothing means "leave nothing of ours behind". Selecting
// a subset by default is how route-guard survived a full uninstall.
func TestHooksUninstallWithoutFlagsRemovesEveryMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := runHooksCapturing(t,
		"install", "--coach", "--read-guard", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := runHooksCapturing(t, "uninstall", "--settings", path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	hooks := hooksMap(readJSON(t, path))
	for _, marker := range hookMarkers {
		if got := countMarker(hooks, marker); got != 0 {
			t.Fatalf("%s should be gone, found %d", marker, got)
		}
	}
}

// Codex writes the whole invocation into `command` as one string. A matcher
// that only knew args[0] reported "no hooks wired" for a file that had them.
func TestHooksStatusAndUninstallSeeCodexCommandStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := runHooksCapturing(t,
		"install", "--client", "codex", "--coach", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := runHooksCapturing(t, "status", "--client", "codex", "--settings", path)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"coach-hook", "route-guard"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status did not report %s for codex:\n%s", want, out)
		}
	}
	if _, err := runHooksCapturing(t,
		"uninstall", "--client", "codex", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	hooks := hooksMap(readJSON(t, path))
	if got := countMarker(hooks, "route-guard"); got != 0 {
		t.Fatalf("route-guard should be gone from codex, found %d", got)
	}
	if got := countMarker(hooks, "coach-hook"); got != 1 {
		t.Fatalf("coach-hook should remain in codex, found %d", got)
	}
}

// Cursor's schema is flat: an event maps straight to entries, with no inner
// "hooks" array and no "type" key.
func TestHooksStatusAndUninstallSeeCursorFlatEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := runHooksCapturing(t,
		"install", "--client", "cursor", "--coach", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := runHooksCapturing(t, "status", "--client", "cursor", "--settings", path)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"coach-hook", "route-guard"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status did not report %s for cursor:\n%s", want, out)
		}
	}
	if _, err := runHooksCapturing(t,
		"uninstall", "--client", "cursor", "--coach", "--settings", path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	hooks := hooksMap(readJSON(t, path))
	if got := countMarker(hooks, "coach-hook"); got != 0 {
		t.Fatalf("coach-hook should be gone from cursor, found %d", got)
	}
	if got := countMarker(hooks, "route-guard"); got != 1 {
		t.Fatalf("route-guard should remain in cursor, found %d", got)
	}
}

// opencode's extension point is a generated module rather than a config file,
// so status has to read the plugin and uninstall has to rewrite it.
func TestHooksStatusReportsOpencodePlugin(t *testing.T) {
	dir := t.TempDir()
	if _, err := runHooksCapturing(t,
		"install", "--client", "opencode", "--coach", "--route-guard", "--settings", dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, err := runHooksCapturing(t, "status", "--client", "opencode", "--settings", dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"coach-hook", "route-guard", opencodePluginName} {
		if !strings.Contains(out, want) {
			t.Fatalf("status did not report %s for opencode:\n%s", want, out)
		}
	}
	// Asserted on the status line, not on the bare verb: the generated file is
	// itself called tokenops-read-guard.ts, so its path mentions a guard that
	// may well not be wired.
	if strings.Contains(out, "read-guard  event=") {
		t.Fatalf("status reported a read-guard that was never installed:\n%s", out)
	}
}

func TestHooksUninstallOpencodeRewritesRemainingHalves(t *testing.T) {
	dir := t.TempDir()
	if _, err := runHooksCapturing(t,
		"install", "--client", "opencode", "--coach", "--read-guard", "--route-guard",
		"--budget", "42", "--settings", dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := runHooksCapturing(t,
		"uninstall", "--client", "opencode", "--route-guard", "--settings", dir); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, opencodePluginName))
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	src := string(b)
	if strings.Contains(src, `run(["route-guard"`) {
		t.Fatalf("route-guard should be gone from the plugin:\n%s", src)
	}
	if !strings.Contains(src, `run(["read-guard"`) {
		t.Fatalf("read-guard should remain in the plugin:\n%s", src)
	}
	// The budget is baked into the coach call; rewriting must not reset it to
	// the default, which would silently change what the operator configured.
	if !strings.Contains(src, `"--budget", "42"`) {
		t.Fatalf("rewritten plugin lost the configured budget:\n%s", src)
	}
}

func TestHooksUninstallOpencodeDeletesTheFileWhenNothingRemains(t *testing.T) {
	dir := t.TempDir()
	if _, err := runHooksCapturing(t,
		"install", "--client", "opencode", "--coach", "--settings", dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := runHooksCapturing(t, "uninstall", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, opencodePluginName)); !os.IsNotExist(err) {
		t.Fatalf("plugin should have been deleted, stat err = %v", err)
	}
}

func TestHooksStatusAndUninstallRejectAnUnknownClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := runHooksCapturing(t, "status", "--client", "emacs", "--settings", path); err == nil {
		t.Fatalf("status accepted an unknown client")
	}
	if _, err := runHooksCapturing(t, "uninstall", "--client", "emacs", "--settings", path); err == nil {
		t.Fatalf("uninstall accepted an unknown client")
	}
}

// Removing by marker across every event is what makes uninstall work for
// clients that spell the event differently — and it is also what could walk
// off with somebody else's hooks if the marker check were loose.
func TestHooksUninstallLeavesForeignEntriesAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{"hooks": []any{
					map[string]any{"type": "command", "command": "/usr/local/bin/somebody-else", "args": []any{"recall"}},
				}},
			},
		},
	}
	if err := writeSettings(path, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := runHooksCapturing(t, "install", "--route-guard", "--settings", path); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := runHooksCapturing(t, "uninstall", "--settings", path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	hooks := hooksMap(readJSON(t, path))
	if countMarker(hooks, "route-guard") != 0 {
		t.Fatalf("route-guard should be gone")
	}
	groups, _ := hooks["UserPromptSubmit"].([]any)
	if len(groups) != 1 {
		t.Fatalf("the foreign UserPromptSubmit hook was dropped: %#v", hooks)
	}
	if n := len(findMarkerEntries(hooks, "recall")); n != 1 {
		t.Fatalf("want the foreign entry intact, found %d", n)
	}
}

// shellQuote wraps a path holding spaces, so the verb is not simply the
// second whitespace-separated field.
func TestCommandVerbSurvivesAQuotedBinaryPath(t *testing.T) {
	cmd := strings.Join([]string{shellQuote("/Applications/My Tools/tokenops"), "route-guard", "--provider", "anthropic"}, " ")
	if got := commandVerb(cmd); got != "route-guard" {
		t.Fatalf("commandVerb(%q) = %q, want route-guard", cmd, got)
	}
}
