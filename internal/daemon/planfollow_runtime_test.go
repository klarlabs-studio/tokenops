package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
)

// claudeHome is a home where Claude Code is signed in on the given tier.
func claudeHome(t *testing.T, tier string) string {
	t.Helper()
	home := t.TempDir()
	body := `{"oauthAccount":{"organizationType":"claude_max","organizationRateLimitTier":"` + tier + `"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// The daemon follows a plan change the client reports without anyone
// re-running init: it rebinds, says so in its log with the command that
// undoes it, and asks to be restarted so the new limits apply. The next
// pass, with nothing new, does nothing.
func TestPlanFollowerRebindsWhatTheClientReports(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("plans:\n  anthropic: claude-ultra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	applied := 0
	f := planFollower{
		configPath: cfgPath,
		home:       claudeHome(t, "default_claude_max_5x"),
		apply:      func() { applied++ },
		logger:     slog.New(slog.NewTextHandler(&logs, nil)),
		now:        func() time.Time { return time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC) },
	}
	f.check(context.Background())
	cfg, err := config.ReadMutable(cfgPath)
	if err != nil || cfg.Plans["anthropic"] != "claude-max" {
		t.Fatalf("plans = %v, %v", cfg.Plans, err)
	}
	if applied != 1 {
		t.Errorf("applied %d times, want 1", applied)
	}
	if out := logs.String(); !strings.Contains(out, "claude-max") ||
		!strings.Contains(out, "tokenops plan set anthropic claude-ultra") {
		t.Errorf("log does not say what changed and how to undo it:\n%s", out)
	}
	logs.Reset()
	f.check(context.Background())
	if applied != 1 || logs.Len() != 0 {
		t.Errorf("a second pass with nothing new acted: applied=%d log=%q", applied, logs.String())
	}
}

// Without a config file there is nothing to rebind into, and the daemon
// does not create one.
func TestPlanFollowRuntimeNeedsAConfigFile(t *testing.T) {
	cfg := config.Default()
	cfg.SourcePath = filepath.Join(t.TempDir(), "missing.yaml")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := lifecycle.New(t.Context(), logger)
	startPlanFollowRuntime(cfg, nil, sup, logger)
	if got := sup.Running(); len(got) != 0 {
		t.Fatalf("runtime registered tasks without a config file: %v", got)
	}
}
