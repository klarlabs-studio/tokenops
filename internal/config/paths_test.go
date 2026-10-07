package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The configuration guide writes paths as ~/.tokenops/...; nothing
// expanded the ~, so the daemon opened a directory literally named "~"
// under its working directory (/ under launchd) and the pricing override
// failed to load.
func TestLoadExpandsHomeInPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "storage:\n  path: ~/.tokenops/events.db\npricing:\n  path: ~/.tokenops/pricing.yaml\n" +
		"rules:\n  root: ~/rules\ntls:\n  cert_dir: ~/certs\n" +
		"vendor_usage:\n  claude_code_jsonl:\n    root: ~/projects\n  codex_jsonl:\n    root: /abs/sessions\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"storage.path":           cfg.Storage.Path,
		"pricing.path":           cfg.Pricing.Path,
		"rules.root":             cfg.Rules.Root,
		"tls.cert_dir":           cfg.TLS.CertDir,
		"claude_code_jsonl.root": cfg.VendorUsage.ClaudeCodeJSONL.Root,
	} {
		if !filepath.IsAbs(got) || filepath.Dir(got) == "~" || got[:len(home)] != home {
			t.Errorf("%s = %q, want it under %s", name, got, home)
		}
	}
	if cfg.VendorUsage.CodexJSONL.Root != "/abs/sessions" {
		t.Errorf("an absolute path changed: %q", cfg.VendorUsage.CodexJSONL.Root)
	}
}

// Writing back must keep what the operator wrote.
func TestReadMutableKeepsTheTilde(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("storage:\n  path: ~/.tokenops/events.db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadMutable(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Path != "~/.tokenops/events.db" {
		t.Errorf("ReadMutable rewrote the path to %q", cfg.Storage.Path)
	}
}
