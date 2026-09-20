package config

import (
	"os"
	"path/filepath"
	"testing"
)

// config.yaml holds every vendor credential TokenOps has been given: the
// claude.ai session, the Cursor cookie, the Copilot OAuth token, the
// Anthropic admin key, the dashboard token. WriteMutable asks for 0600
// and gets it — but only on a file it creates. os.WriteFile applies its
// mode at creation and leaves an existing file's mode alone.
//
// A config that predates the 0600 default, or one an operator created by
// hand, therefore keeps its old mode while `vendor-usage setup` writes a
// session key into it. Nothing in the write path noticed.
func TestWriteMutableTightensAnExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:7070\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := Default()
	cfg.VendorUsage.ClaudeUsageMeter.SessionKey = "sk-ant-sid-whatever"
	if err := WriteMutable(path, cfg); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %#o after writing a session key, want 0600", perm)
	}
}

// The directory has the same problem: ~/.config/tokenops created by
// something other than `tokenops init` keeps whatever mode it was given.
func TestWriteMutableTightensAnExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tokenops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := filepath.Join(dir, "config.yaml")

	if err := WriteMutable(path, Default()); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir mode = %#o, want 0700", perm)
	}
}

// A fresh write must still land at 0600 — the existing guarantee.
func TestWriteMutableCreatesAtPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := WriteMutable(path, Default()); err != nil {
		t.Fatalf("WriteMutable: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("new config mode = %#o, want 0600", perm)
	}
}

// Validation runs before anything touches the file. An invalid mutation
// must not leave a half-written config, and must not change the mode of
// the one already there either.
func TestWriteMutableRejectsInvalidBeforeTouchingTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const original = "listen: 127.0.0.1:7070\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bad := Default()
	bad.Listen = ""
	if err := WriteMutable(path, bad); err == nil {
		t.Fatal("an invalid config was written")
	}

	body, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != original {
		t.Errorf("a rejected write changed the file:\n%s", body)
	}
}
