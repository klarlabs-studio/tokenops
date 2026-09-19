package cli

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// The MCP dashboard link falls back to this when the URL hint is gone, so
// it must resolve the token the daemon actually accepts: a configured admin
// token first, else the one the daemon minted and saved.
func TestDashboardTokenForResolvesLikeTheDaemon(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "tokenops"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "tokenops", "dashboard.token"), []byte("minted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dashboardTokenFor(config.Config{}); got != "minted" {
		t.Errorf("saved token = %q, want minted", got)
	}
	cfg := config.Config{Dashboard: config.DashboardConfig{AdminToken: "configured"}}
	if got := dashboardTokenFor(cfg); got != "configured" {
		t.Errorf("token = %q, want the configured admin token to win", got)
	}
}
