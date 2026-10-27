package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/daemon"
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

// Applying the unit replaces the running daemon, so a failure can leave
// none at all. That must not exit 0 — the command used to print a hint and
// report success, so a script or setup flow carried on without ingestion —
// and the way out it names is a tokenops command, not supervisor syntax.
func TestDaemonInstallFailsWhenTheUnitCannotBeStarted(t *testing.T) {
	prev := applyUnit
	applyUnit = func(daemon.UnitKind, string, bool) error {
		return errors.New("launchctl bootstrap: exit status 5")
	}
	t.Cleanup(func() { applyUnit = prev })

	out, err := executeRoot(t,
		"daemon", "install",
		"--kind", "launchd",
		"--bin", "/opt/homebrew/bin/tokenops",
		"--home", "/Users/ada",
		"--path", filepath.Join(t.TempDir(), "de.klarlabs.tokenops.plist"),
	)
	if err == nil {
		t.Fatalf("install reported success although the daemon did not start:\n%s", out)
	}
	if strings.Contains(out, "launchctl") {
		t.Errorf("failure hands the operator supervisor commands:\n%s", out)
	}
	if !strings.Contains(out, "tokenops daemon install") {
		t.Errorf("failure should name the command that fixes it:\n%s", out)
	}
}

// Reinstalling an unchanged unit tells the supervisor so: that is the case
// where replacing the process in place is enough.
func TestDaemonInstallPassesWhetherTheUnitChanged(t *testing.T) {
	var seen []bool
	prev := applyUnit
	prevAwait := awaitDaemonHealth
	applyUnit = func(_ daemon.UnitKind, _ string, changed bool) error {
		seen = append(seen, changed)
		return nil
	}
	awaitDaemonHealth = func(context.Context, string, time.Duration) (bool, error) { return false, nil }
	t.Cleanup(func() {
		applyUnit = prev
		awaitDaemonHealth = prevAwait
	})

	args := []string{
		"daemon", "install",
		"--kind", "launchd",
		"--bin", "/opt/homebrew/bin/tokenops",
		"--home", "/Users/ada",
		"--path", filepath.Join(t.TempDir(), "de.klarlabs.tokenops.plist"),
	}
	for range 2 {
		if out, err := executeRoot(t, args...); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
	}
	if !slices.Equal(seen, []bool{true, false}) {
		t.Errorf("changed = %v, want [true false]: written, then already up to date", seen)
	}
}

// stubStartRetry replaces the supervisor restart and the pause before it,
// so no test kicks the real launchd job. It returns a pointer to the
// number of restarts asked for.
func stubStartRetry(t *testing.T) *int {
	t.Helper()
	prevRestart, prevPause := restartUnit, startRetryPause
	restarts := 0
	restartUnit = func(daemon.UnitKind) error { restarts++; return nil }
	startRetryPause = 0
	t.Cleanup(func() {
		restartUnit = prevRestart
		startRetryPause = prevPause
	})
	return &restarts
}

func TestDaemonInstallDoesNotClaimRunningBeforeHealthAnswers(t *testing.T) {
	prevApply, prevAwait := applyUnit, awaitDaemonHealth
	applyUnit = func(daemon.UnitKind, string, bool) error { return nil }
	awaitDaemonHealth = func(context.Context, string, time.Duration) (bool, error) {
		return false, errors.New("connection refused")
	}
	t.Cleanup(func() {
		applyUnit = prevApply
		awaitDaemonHealth = prevAwait
	})
	restarts := stubStartRetry(t)

	out, err := executeRoot(t,
		"daemon", "install",
		"--kind", "launchd",
		"--bin", "/opt/homebrew/bin/tokenops",
		"--home", "/Users/ada",
		"--path", filepath.Join(t.TempDir(), "de.klarlabs.tokenops.plist"),
	)
	if err == nil {
		t.Fatalf("install reported success before health answered:\n%s", out)
	}
	if strings.Contains(out, "Running and healthy") {
		t.Fatalf("install claimed a healthy daemon:\n%s", out)
	}
	if *restarts != 1 {
		t.Errorf("restarts = %d, want exactly one retry before giving up", *restarts)
	}
}

// After an upgrade, launchd has refused the first start of a new binary
// (OS_REASON_CODESIGNING, "spawn failed") and left the daemon down, while
// the next start of the same file worked. One retry recovers that.
func TestDaemonInstallRetriesAStartThatNeverAnswered(t *testing.T) {
	prevApply, prevAwait := applyUnit, awaitDaemonHealth
	applyUnit = func(daemon.UnitKind, string, bool) error { return nil }
	probes := 0
	awaitDaemonHealth = func(context.Context, string, time.Duration) (bool, error) {
		probes++
		if probes == 1 {
			return false, errors.New("connection refused")
		}
		return false, nil
	}
	t.Cleanup(func() {
		applyUnit = prevApply
		awaitDaemonHealth = prevAwait
	})
	restarts := stubStartRetry(t)

	out, err := executeRoot(t,
		"daemon", "install",
		"--kind", "launchd",
		"--bin", "/opt/homebrew/bin/tokenops",
		"--home", "/Users/ada",
		"--path", filepath.Join(t.TempDir(), "de.klarlabs.tokenops.plist"),
	)
	if err != nil {
		t.Fatalf("install failed although the retry came up: %v\n%s", err, out)
	}
	if *restarts != 1 || probes != 2 {
		t.Errorf("restarts = %d, probes = %d, want 1 and 2", *restarts, probes)
	}
	if !strings.Contains(out, "Running and healthy") {
		t.Errorf("install did not report the recovered daemon:\n%s", out)
	}
}
