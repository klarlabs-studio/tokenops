package cli

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
)

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fmt tests need a POSIX shell")
	}
}

func testRegistry() *formatter.Registry {
	return formatter.NewRegistry(
		formatter.LossPolicy{Default: formatter.LossBalanced},
		formatter.NewGit(),
	)
}

func TestRunFmt_GenericCompressesAndRecovers(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	argv := []string{"sh", "-c", `printf 'a\na\n\n\n\nb\n'`}
	res, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{RecoverDir: dir})
	if err != nil {
		t.Fatalf("runFmt: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
	// Duplicate "a" and blank run collapsed by the generic scrub.
	if strings.Count(string(res.Stdout), "a") != 1 {
		t.Errorf("duplicate line not collapsed: %q", res.Stdout)
	}
	if res.BytesAfter >= res.BytesBefore {
		t.Errorf("no compression: before=%d after=%d", res.BytesBefore, res.BytesAfter)
	}
	if res.RecoveryPath == "" {
		t.Fatal("recovery path empty")
	}
	raw, err := os.ReadFile(res.RecoveryPath)
	if err != nil {
		t.Fatalf("read recovery: %v", err)
	}
	// Recovery must hold the FULL original output, not the compact form.
	if !strings.Contains(string(raw), "## stdout") {
		t.Errorf("recovery missing stdout section: %q", raw)
	}
}

func TestRunFmt_PropagatesExitCode(t *testing.T) {
	skipOnWindows(t)
	argv := []string{"sh", "-c", "exit 3"}
	res, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{NoRecover: true})
	if err != nil {
		t.Fatalf("runFmt: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", res.ExitCode)
	}
}

func TestRunFmt_RawOnError(t *testing.T) {
	skipOnWindows(t)
	// Command emits compressible stdout but exits non-zero: with
	// RawOnError the stdout must be forwarded uncompressed.
	argv := []string{"sh", "-c", `printf 'x\nx\n\n\n\ny\n'; exit 1`}
	res, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{NoRecover: true, RawOnError: true})
	if err != nil {
		t.Fatalf("runFmt: %v", err)
	}
	if res.BytesAfter != res.BytesBefore {
		t.Errorf("stdout was compressed despite RawOnError: before=%d after=%d", res.BytesBefore, res.BytesAfter)
	}
	if !strings.Contains(res.Notes, "non-zero exit") {
		t.Errorf("expected raw-on-error note, got %q", res.Notes)
	}
}

func TestRunFmt_LaunchFailureIsError(t *testing.T) {
	skipOnWindows(t)
	argv := []string{"this-command-does-not-exist-xyz"}
	_, err := runFmt(context.Background(), testRegistry(), argv, fmtOptions{NoRecover: true})
	if err == nil {
		t.Fatal("expected error launching missing command")
	}
}

func TestBuildLossPolicy(t *testing.T) {
	cfg := config.CommandFmtConfig{
		Default:   "balanced",
		Overrides: map[string]string{"docker": "aggressive", "git": "conservative"},
	}
	p, warn := buildLossPolicy(cfg, "")
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
	if p.Default != formatter.LossBalanced {
		t.Errorf("default = %v, want balanced", p.Default)
	}
	if p.LevelFor("docker") != formatter.LossAggressive {
		t.Error("docker override not applied")
	}
	if p.LevelFor("git") != formatter.LossConservative {
		t.Error("git override not applied")
	}
	// Unknown command falls back to default.
	if p.LevelFor("npm") != formatter.LossBalanced {
		t.Error("default not applied to unknown command")
	}
}

func TestBuildLossPolicy_RunOverrideWins(t *testing.T) {
	cfg := config.CommandFmtConfig{
		Default:   "conservative",
		Overrides: map[string]string{"docker": "aggressive"},
	}
	p, _ := buildLossPolicy(cfg, "balanced")
	// --level replaces default and clears per-command overrides.
	if p.LevelFor("docker") != formatter.LossBalanced {
		t.Errorf("run override should win over per-command; got %v", p.LevelFor("docker"))
	}
	if p.LevelFor("anything") != formatter.LossBalanced {
		t.Errorf("run override should be the default; got %v", p.LevelFor("anything"))
	}
}

func TestBuildLossPolicy_InvalidWarns(t *testing.T) {
	cfg := config.CommandFmtConfig{Default: "bogus"}
	_, warn := buildLossPolicy(cfg, "")
	if warn == "" {
		t.Error("expected warning for invalid default level")
	}
}

func TestEstTokens(t *testing.T) {
	if estTokens(0) != 0 || estTokens(-5) != 0 {
		t.Error("non-positive delta should yield 0 tokens")
	}
	if estTokens(40) != 10 {
		t.Errorf("estTokens(40) = %d, want 10", estTokens(40))
	}
}
