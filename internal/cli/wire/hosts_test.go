package wire

import (
	"os"
	"path/filepath"
	"testing"
)

// Only hosts that actually exist are wired. Creating a config for a client
// the operator does not have would leave dead entries on their machine.
func TestDiscoverSkipsAbsentHosts(t *testing.T) {
	home := t.TempDir()
	got := DiscoverHosts(home)
	if len(got) != 0 {
		t.Errorf("hosts = %+v, want none on an empty home", got)
	}
}

// Claude Code keeps its MCP servers in ~/.claude.json.
func TestDiscoverFindsClaudeCode(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := DiscoverHosts(home)
	if len(got) != 1 || got[0].Name != "Claude Code" {
		t.Fatalf("hosts = %+v, want Claude Code", got)
	}
}

// Claude Desktop keeps its own config elsewhere; both are wired when both
// are present, so one init covers the machine.
func TestDiscoverFindsMultipleHosts(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	desktop := filepath.Join(home, "Library", "Application Support", "Claude")
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(desktop, "claude_desktop_config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := DiscoverHosts(home)
	if len(got) != 2 {
		t.Fatalf("hosts = %+v, want 2", got)
	}
}
