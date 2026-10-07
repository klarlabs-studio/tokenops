package codexappserver

import (
	"os"
	"path/filepath"
	"testing"
)

// launchd's PATH hides ~/.local/bin, where Codex's installer puts it.
func TestLocate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	if _, ok := Locate(home); ok {
		t.Fatal("found a codex that is not there")
	}
	bin := filepath.Join(home, ".local", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Locate(home); !ok || got != bin {
		t.Errorf("locate = %q, %v", got, ok)
	}
}
