package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runPlanSet exercises the plan set command end-to-end against a
// temp-dir config. Returns the recorded output for hint assertions.
func runPlanSet(t *testing.T, configPath, provider, planName string) string {
	t.Helper()
	cmd := newPlanSetCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--config-path", configPath, provider, planName})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan set: %v\noutput: %s", err, out.String())
	}
	return out.String()
}

// seedConfig writes a minimal init-style config file so set/unset have
// something to mutate without booting the full init wizard.
func seedConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte("listen: 127.0.0.1:7878\nlog:\n  level: info\n  format: text\nshutdown:\n  timeout: 15s\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return path
}

func TestPlanSetWritesProviderBinding(t *testing.T) {
	path := seedConfig(t)
	output := runPlanSet(t, path, "anthropic", "claude-max-20x")

	cfg, err := readMutableConfig(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := cfg.Plans["anthropic"]; got != "claude-max-20x" {
		t.Errorf("plans.anthropic=%q want claude-max-20x", got)
	}
	if !strings.Contains(output, "claude-max-20x") {
		t.Errorf("expected plan name in output: %s", output)
	}
	if !strings.Contains(output, "reload your MCP server") {
		t.Errorf("expected reload hint in output: %s", output)
	}
}

func TestPlanSetRejectsUnknownPlan(t *testing.T) {
	path := seedConfig(t)
	cmd := newPlanSetCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--config-path", path, "anthropic", "claude-maxx"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown plan")
	}
}

func TestPlanSetUpdatesExisting(t *testing.T) {
	path := seedConfig(t)
	runPlanSet(t, path, "anthropic", "claude-pro")
	output := runPlanSet(t, path, "anthropic", "claude-max-20x")
	if !strings.Contains(output, "claude-pro -> claude-max-20x") {
		t.Errorf("expected upgrade message, got: %s", output)
	}
}

func TestPlanUnsetRemovesBinding(t *testing.T) {
	path := seedConfig(t)
	runPlanSet(t, path, "anthropic", "claude-max-20x")

	cmd := newPlanUnsetCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--config-path", path, "anthropic"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan unset: %v", err)
	}
	cfg, _ := readMutableConfig(path)
	if _, ok := cfg.Plans["anthropic"]; ok {
		t.Errorf("anthropic still present after unset")
	}
}

func TestPlanSetMissingConfigFile(t *testing.T) {
	cmd := newPlanSetCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"--config-path", filepath.Join(t.TempDir(), "no-such.yaml"),
		"anthropic", "claude-max-20x",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when config missing")
	}
	if !strings.Contains(err.Error(), "tokenops init") {
		t.Errorf("error should point user at `tokenops init`: %v", err)
	}
}

func TestProviderSetAndUnset(t *testing.T) {
	path := seedConfig(t)

	setCmd := newProviderSetCmd()
	setOut := &bytes.Buffer{}
	setCmd.SetOut(setOut)
	setCmd.SetErr(setOut)
	setCmd.SetArgs([]string{"--config-path", path, "anthropic", "https://api.anthropic.com"})
	if err := setCmd.Execute(); err != nil {
		t.Fatalf("provider set: %v", err)
	}
	cfg, _ := readMutableConfig(path)
	if cfg.Providers["anthropic"] != "https://api.anthropic.com" {
		t.Errorf("provider not written: %+v", cfg.Providers)
	}

	unsetCmd := newProviderUnsetCmd()
	unsetOut := &bytes.Buffer{}
	unsetCmd.SetOut(unsetOut)
	unsetCmd.SetErr(unsetOut)
	unsetCmd.SetArgs([]string{"--config-path", path, "anthropic"})
	if err := unsetCmd.Execute(); err != nil {
		t.Fatalf("provider unset: %v", err)
	}
	cfg, _ = readMutableConfig(path)
	if _, ok := cfg.Providers["anthropic"]; ok {
		t.Errorf("provider still present after unset")
	}
}
