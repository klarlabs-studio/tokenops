package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/mcp"
)

// `tokenops status` words its source warnings from the daemon's own
// assessment when the daemon gives one, as the MCP status tool does, and
// falls back to the event counts when it does not.
func TestStatusStaleWarningsPreferDaemonHealth(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".tokenops"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("XDG_DATA_HOME", home+"/.local/share")
	t.Setenv("GEMINI_CLI_HOME", "")
	cfg := config.Default()
	cfg.VendorUsage.GeminiCLI.Enabled = true

	idle := func(string) (mcp.DaemonSources, error) {
		return mcp.DaemonSources{Sources: []state.SourceReport{
			{Tag: "gemini-cli", State: freshness.StateIdle, Severity: freshness.SeverityOK},
		}}, nil
	}
	if got := statusStaleWarnings(context.Background(), nil, cfg, "http://127.0.0.1:1", idle); len(got) != 0 {
		t.Errorf("warnings with an idle reader = %q, want none", got)
	}

	refused := func(string) (mcp.DaemonSources, error) {
		return mcp.DaemonSources{Sources: []state.SourceReport{
			{Tag: "gemini-cli", State: freshness.StateFailing, Severity: freshness.SeverityWarning, LastError: "401"},
		}}, nil
	}
	got := statusStaleWarnings(context.Background(), nil, cfg, "http://127.0.0.1:1", refused)
	if len(got) != 1 || !strings.Contains(got[0], "gemini-cli is being refused (401)") {
		t.Errorf("warnings with a refused reader = %q", got)
	}

	down := func(string) (mcp.DaemonSources, error) { return mcp.DaemonSources{}, errors.New("401") }
	got = statusStaleWarnings(context.Background(), nil, cfg, "http://127.0.0.1:1", down)
	if len(got) != 1 || !strings.Contains(got[0], "ingestion stale [warning]: gemini-cli") {
		t.Errorf("warnings without the daemon's answer = %q, want the count check", got)
	}
}
