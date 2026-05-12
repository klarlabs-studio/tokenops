package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// runInitInDir executes the init command with the given args and a
// scratch directory pinned via flags so the test never touches the
// developer's real $HOME.
func runInitInDir(t *testing.T, args ...string) (configPath, storagePath, stdout string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config.yaml")
	storagePath = filepath.Join(dir, "data", "events.db")

	cmd := newInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	full := append([]string{
		"--config-path", configPath,
		"--storage-path", storagePath,
		"--rules-root", dir,
		"--repo-id", "test-repo",
	}, args...)
	cmd.SetArgs(full)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out.String())
	}
	return configPath, storagePath, out.String()
}

func TestInitWritesValidConfig(t *testing.T) {
	configPath, storagePath, _ := runInitInDir(t)

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if !cfg.Storage.Enabled || cfg.Storage.Path != storagePath {
		t.Errorf("storage not wired: %+v", cfg.Storage)
	}
	if !cfg.Rules.Enabled || cfg.Rules.RepoID != "test-repo" {
		t.Errorf("rules not wired: %+v", cfg.Rules)
	}
	if cfg.Listen == "" {
		t.Error("expected Listen defaulted")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("written config fails Validate(): %v", err)
	}
	blockers := cfg.Blockers()
	// Providers still empty so providers_unconfigured is expected, but
	// storage_disabled / rules_disabled must be gone.
	for _, b := range blockers {
		if b == "storage_disabled" || b == "rules_disabled" {
			t.Errorf("init left blocker %q in place", b)
		}
	}
}

func TestInitIsIdempotent(t *testing.T) {
	configPath, _, _ := runInitInDir(t)

	// Re-run with the same flags. Without --force, the second run must
	// not overwrite the file and must not return an error.
	cmd := newInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	dir := filepath.Dir(configPath)
	cmd.SetArgs([]string{
		"--config-path", configPath,
		"--storage-path", filepath.Join(dir, "events.db"),
		"--rules-root", dir,
		"--repo-id", "test-repo",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("re-run init: %v", err)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Errorf("expected idempotent skip message, got: %s", out.String())
	}
}

func TestInitForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	storagePath := filepath.Join(dir, "events.db")
	args := make([]string, 0, 9)
	args = append(args,
		"--config-path", configPath,
		"--storage-path", storagePath,
		"--rules-root", dir,
		"--repo-id", "test-repo",
	)

	first := newInitCmd()
	first.SetOut(&bytes.Buffer{})
	first.SetErr(&bytes.Buffer{})
	first.SetArgs(args)
	if err := first.Execute(); err != nil {
		t.Fatalf("first init: %v", err)
	}
	originalStat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Pollute the file to prove --force actually rewrites it. Without
	// --force the test would fail because the noop branch returns
	// before WriteFile.
	if err := os.WriteFile(configPath, []byte("# stale\n"), 0o600); err != nil {
		t.Fatalf("pollute: %v", err)
	}

	second := newInitCmd()
	second.SetOut(&bytes.Buffer{})
	second.SetErr(&bytes.Buffer{})
	second.SetArgs(append(args, "--force"))
	if err := second.Execute(); err != nil {
		t.Fatalf("force init: %v", err)
	}
	newStat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if newStat.Size() != originalStat.Size() {
		t.Errorf("force-rewritten size mismatch: %d -> %d", originalStat.Size(), newStat.Size())
	}
}

func TestInitPrintOnlyDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	cmd := newInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"--config-path", configPath,
		"--storage-path", filepath.Join(dir, "events.db"),
		"--rules-root", dir,
		"--repo-id", "test-repo",
		"--print-only",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("print-only init: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("print-only should not have written config: %v", err)
	}
	if !strings.Contains(out.String(), "storage:") {
		t.Errorf("expected YAML on stdout, got: %s", out.String())
	}
}
