package wire

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

func serverEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	root := readJSON(t, path)
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("no mcpServers in %s: %v", path, root)
	}
	entry, ok := servers["tokenops"].(map[string]any)
	if !ok {
		t.Fatalf("no tokenops server in %s: %v", servers, path)
	}
	return entry
}

// A host with no MCP config at all gets one.
func TestRegistersInFreshHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	res, err := EnsureMCPServer(path, "/opt/bin/tokenops")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !res.Changed {
		t.Error("fresh host should report a change")
	}
	if got := serverEntry(t, path)["command"]; got != "/opt/bin/tokenops" {
		t.Errorf("command = %v", got)
	}
}

// The bug this exists to prevent: an entry pointing at a bare name resolves
// through PATH, which is how the MCP server ended up running a stale binary
// while the daemon ran a current one. Registration pins the absolute path.
func TestRepointsBareCommandToAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	seed := map[string]any{"mcpServers": map[string]any{
		"tokenops": map[string]any{"type": "stdio", "command": "tokenops", "args": []any{"serve"}},
	}}
	b, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := EnsureMCPServer(path, "/new/bin/tokenops")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !res.Changed {
		t.Error("a bare command must be repointed")
	}
	if res.PreviousCommand != "tokenops" {
		t.Errorf("PreviousCommand = %q, want the stale value for reporting", res.PreviousCommand)
	}
	if got := serverEntry(t, path)["command"]; got != "/new/bin/tokenops" {
		t.Errorf("command = %v", got)
	}
}

// Re-running init must be a no-op, not a rewrite. Reporting a change every
// time would make "what did init actually do" meaningless.
func TestIdempotentWhenAlreadyCorrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	if _, err := EnsureMCPServer(path, "/opt/bin/tokenops"); err != nil {
		t.Fatalf("first: %v", err)
	}
	res, err := EnsureMCPServer(path, "/opt/bin/tokenops")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if res.Changed {
		t.Error("second run should be a no-op")
	}
}

// Other servers in the host config are the user's, not ours.
func TestPreservesOtherServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	seed := map[string]any{
		"someOtherKey": "preserved",
		"mcpServers": map[string]any{
			"github": map[string]any{"command": "gh-mcp"},
		},
	}
	b, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := EnsureMCPServer(path, "/opt/bin/tokenops"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	root := readJSON(t, path)
	if root["someOtherKey"] != "preserved" {
		t.Error("unrelated top-level key lost")
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Error("another user's MCP server was dropped")
	}
}

// We are editing someone else's file, so the prior version is kept.
func TestBacksUpPriorConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := EnsureMCPServer(path, "/opt/bin/tokenops"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("no backup written: %v", err)
	}
}

// Refuse to clobber a file we cannot parse — it is the user's config and a
// truncated write would cost them every other server they had.
func TestRefusesMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := EnsureMCPServer(path, "/opt/bin/tokenops"); err == nil {
		t.Error("expected a refusal for unparseable config")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{not json" {
		t.Error("malformed config was modified")
	}
}
