package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
)

// isolateMachine points the home, every XDG root and PATH at temp dirs, so
// the probes see only what the test made. HOME alone does not isolate:
// the config is found through XDG_CONFIG_HOME.
func isolateMachine(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, filepath.Join(home, "."+v))
	}
	t.Setenv("PATH", t.TempDir())
	return home
}

// The daemon's connections read the tools on this machine and the
// dismissals as the config file has them now.
func TestConnectionsFn(t *testing.T) {
	home := isolateMachine(t)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	g := headroom.Glance{Turns: map[string]plans.ProviderTurns{"cursor": {Turns: 4, LastAt: now.Add(-time.Hour)}}}
	machine := func() sourceprobe.Machine {
		m := sourceprobe.ThisMachine()
		m.AppDirs = []string{filepath.Join(home, "Applications")}
		return m
	}
	fn := connectionsFn(config.Config{SourcePath: path}, machine)

	got := fn(g, nil, now)
	if len(got) != 1 || got[0].Provider != "cursor" || got[0].State != connections.NotConnected || len(got[0].Evidence) != 2 {
		t.Fatalf("connections = %+v", got)
	}
	if got[0].Evidence[1].Detail != "~/.cursor" {
		t.Errorf("installed evidence = %q, want the path with ~ for the home", got[0].Evidence[1].Detail)
	}

	if err := os.WriteFile(path, []byte("vendor_usage:\n  dismissed: [cursor]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := fn(g, nil, now); len(got) != 1 || got[0].State != connections.Dismissed {
		t.Errorf("after the dismissal was written: %+v", got)
	}
}
