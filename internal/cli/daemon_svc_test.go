package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonInstallDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokenops.service")
	out, err := executeRoot(t,
		"daemon", "install",
		"--kind", "systemd",
		"--bin", "/usr/bin/tokenops",
		"--home", "/home/ada",
		"--path", path,
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote %s", path)
	}
	if !strings.Contains(out, "ExecStart=/usr/bin/tokenops start") {
		t.Fatalf("dry-run missing rendered unit:\n%s", out)
	}
	if strings.Contains(out, "__TOKENOPS_BIN__") {
		t.Fatal("dry-run still has placeholders")
	}
}

func TestDaemonInstallNoLoadWritesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "de.klarlabs.tokenops.plist")
	args := []string{
		"daemon", "install",
		"--kind", "launchd",
		"--bin", "/opt/homebrew/bin/tokenops",
		"--home", "/Users/ada",
		"--path", path,
		"--no-load",
	}
	out, err := executeRoot(t, args...)
	if err != nil {
		t.Fatalf("install 1: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Wrote "+path) {
		t.Fatalf("first install should write:\n%s", out)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/opt/homebrew/bin/tokenops") {
		t.Fatalf("unit missing binary:\n%s", body)
	}
	if strings.Contains(string(body), "__TOKENOPS_BIN__") {
		t.Fatal("installed unit still has placeholders")
	}

	out, err = executeRoot(t, args...)
	if err != nil {
		t.Fatalf("install 2: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Already up to date") {
		t.Fatalf("second install should be a no-op:\n%s", out)
	}
}

func TestDaemonUninstallRemovesUnit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokenops.service")
	if _, err := executeRoot(t,
		"daemon", "install",
		"--kind", "systemd",
		"--bin", "/usr/bin/tokenops",
		"--home", "/home/ada",
		"--path", path,
		"--no-load",
	); err != nil {
		t.Fatal(err)
	}
	out, err := executeRoot(t,
		"daemon", "uninstall",
		"--kind", "systemd",
		"--home", "/home/ada",
		"--path", path,
		"--no-load",
	)
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unit still present: %v", err)
	}
}

func TestDaemonStatusReportsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.plist")
	out, err := executeRoot(t,
		"daemon", "status",
		"--kind", "launchd",
		"--bin", "/bin/tokenops",
		"--home", dir,
		"--path", path,
	)
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not installed") {
		t.Fatalf("status should say not installed:\n%s", out)
	}
}
