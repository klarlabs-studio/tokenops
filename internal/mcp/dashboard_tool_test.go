package mcp

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readURLHint returns a typed error when the hint is missing so the
// MCP tool can branch on "daemon not running" cleanly. It must parse
// a well-formed payload otherwise.
func TestReadURLHint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	// Missing file: must return os.ErrNotExist (callers branch on it).
	if _, err := readURLHint(); !os.IsNotExist(err) {
		t.Fatalf("missing hint: want os.ErrNotExist, got %v", err)
	}

	// Well-formed file: parse all fields.
	payload := urlHintPayload{
		URL:       "http://127.0.0.1:8080",
		Addr:      "127.0.0.1:8080",
		TLS:       false,
		PID:       4242,
		StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	dst := filepath.Join(dir, "tokenops")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "daemon.url"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readURLHint()
	if err != nil {
		t.Fatalf("readURLHint: %v", err)
	}
	if got.URL != payload.URL || got.PID != payload.PID {
		t.Errorf("payload mismatch: got %+v want %+v", got, payload)
	}
}

func newDashboardServer(t *testing.T, d DashboardDeps) *Server {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterDashboardTool(srv, d); err != nil {
		t.Fatalf("RegisterDashboardTool: %v", err)
	}
	return srv
}

// The dashboard used to exist only as far as the hint file did. With the
// file gone and the daemon still serving, the tool told the operator to
// start a daemon that was already running.
func TestDashboardFindsDaemonWithoutHint(t *testing.T) {
	isolateHint(t)
	d := healthyDaemon(t)
	srv := newDashboardServer(t, DashboardDeps{DaemonURL: d.URL})

	out := execTool(t, srv, "tokenops_dashboard", nil)
	if strings.Contains(out, "daemon_not_running") {
		t.Fatalf("reported not running while the daemon answers: %s", out)
	}
	if !strings.Contains(out, d.URL+"/dashboard") {
		t.Errorf("no dashboard link at the configured address: %s", out)
	}
}

// With no daemon anywhere, the hint names the command that fits this
// machine — never a foreground `tokenops start` beside a supervisor.
func TestDashboardHintWhenAbsentFollowsSupervision(t *testing.T) {
	isolateHint(t)
	dead := deadURL(t)

	supervised := execTool(t, newDashboardServer(t, DashboardDeps{
		DaemonURL: dead, UnitInstalled: func() bool { return true },
	}), "tokenops_dashboard", nil)
	if !strings.Contains(supervised, "daemon_not_running") || !strings.Contains(supervised, "tokenops daemon restart") {
		t.Errorf("supervised machine: want `tokenops daemon restart`: %s", supervised)
	}
	if strings.Contains(supervised, "tokenops start") {
		t.Errorf("supervised machine told to run `tokenops start`: %s", supervised)
	}

	unsupervised := execTool(t, newDashboardServer(t, DashboardDeps{
		DaemonURL: dead, UnitInstalled: func() bool { return false },
	}), "tokenops_dashboard", nil)
	if !strings.Contains(unsupervised, "tokenops daemon install") {
		t.Errorf("unsupervised machine: want `tokenops daemon install`: %s", unsupervised)
	}
}

// urlHintPath should land under tokenops/daemon.url regardless of
// which env var drives it, so the daemon writer and MCP reader
// always agree on location.
func TestURLHintPathHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	p, err := urlHintPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join("tokenops", "daemon.url")) {
		t.Errorf("path missing tokenops/daemon.url suffix: %q", p)
	}
	if !strings.HasPrefix(p, xdg) {
		t.Errorf("path should start with XDG_DATA_HOME (%s): %q", xdg, p)
	}
}

// Found without its hint, the daemon still needs the token the hint used to
// carry; the saved one keeps the link usable instead of sending the operator
// to a login wall.
func TestDashboardWithoutHintCarriesTheSavedToken(t *testing.T) {
	isolateHint(t)
	d := healthyDaemon(t)
	out := execTool(t, newDashboardServer(t, DashboardDeps{
		DaemonURL: d.URL, Token: func() string { return "tok-123" },
	}), "tokenops_dashboard", nil)
	if !strings.Contains(out, d.URL+"/dashboard?token=tok-123") {
		t.Errorf("link without the saved token: %s", out)
	}
}
