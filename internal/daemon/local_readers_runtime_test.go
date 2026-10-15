package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

func readerFollower(t *testing.T, body string, running map[string]bool, present ...string) (*planFollower, string, *int, *bytes.Buffer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	applied := 0
	var logs bytes.Buffer
	f := &planFollower{
		configPath: cfgPath,
		home:       t.TempDir(),
		apply:      func() { applied++ },
		logger:     slog.New(slog.NewTextHandler(&logs, nil)),
		now:        func() time.Time { return time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC) },
		running:    running,
		present:    func() []string { return present },
	}
	return f, cfgPath, &applied, &logs
}

// A client's sessions that appear after the daemon started (opencode
// installed later, or a store an older version could not read) are read
// without anyone re-running init: the daemon turns the reader on, says so
// once with the command that undoes it, and restarts so it runs.
func TestDaemonTurnsOnAReaderWhoseDataAppears(t *testing.T) {
	f, cfgPath, applied, logs := readerFollower(t,
		"vendor_usage:\n  claude_code_jsonl:\n    enabled: true\n  opencode:\n    enabled: false\n",
		map[string]bool{"claude-code-jsonl": true}, "claude-code-jsonl", "opencode")

	f.check(context.Background())

	cfg, err := config.ReadMutable(cfgPath)
	if err != nil || !cfg.VendorUsage.OpenCode.Enabled {
		t.Fatalf("opencode not turned on: %+v, %v", cfg.VendorUsage.OpenCode, err)
	}
	if *applied != 1 {
		t.Errorf("restarted %d times, want 1", *applied)
	}
	if out := logs.String(); !strings.Contains(out, "opencode") || !strings.Contains(out, "--disable") {
		t.Errorf("log does not say what was turned on and how to stop it:\n%s", out)
	}
	logs.Reset()
	f.check(context.Background())
	if *applied != 1 || logs.Len() != 0 {
		t.Errorf("a second pass acted again: applied=%d log=%q", *applied, logs.String())
	}
}

// A reader the operator switched off stays off, whatever is on disk.
func TestDaemonLeavesASwitchedOffReaderOff(t *testing.T) {
	f, cfgPath, applied, _ := readerFollower(t,
		"vendor_usage:\n  opencode:\n    enabled: false\n    switched_off: true\n",
		map[string]bool{}, "opencode")

	f.check(context.Background())

	if cfg, _ := config.ReadMutable(cfgPath); cfg.VendorUsage.OpenCode.Enabled {
		t.Error("a switched-off reader was turned back on")
	}
	if *applied != 0 {
		t.Errorf("restarted %d times for nothing", *applied)
	}
}

// `tokenops init` (or a hand edit) turned a reader on in the file while
// this daemon runs without it: the daemon restarts so the reader runs, and
// asks once rather than every pass.
func TestDaemonPicksUpAReaderTurnedOnInTheFile(t *testing.T) {
	f, _, applied, _ := readerFollower(t,
		"vendor_usage:\n  opencode:\n    enabled: true\n",
		map[string]bool{}, "opencode")

	f.check(context.Background())
	f.check(context.Background())

	if *applied != 1 {
		t.Errorf("restarted %d times, want once", *applied)
	}
}
