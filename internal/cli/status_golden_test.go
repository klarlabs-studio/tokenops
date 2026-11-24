package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/mcp"
)

// cursorNotConnected is the daemon's answer on a machine where Cursor is
// in use and its plan is not read.
func cursorNotConnected() []connections.Connection {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	return connections.Compute(connections.Inputs{Now: now,
		Turns:     map[string]plans.ProviderTurns{"cursor": {Turns: 41, LastAt: now.Add(-2 * time.Hour)}},
		Installed: map[string][]string{"cursor": {"/Applications/Cursor.app"}}})
}

// TestStatusOutputCharacterization pins `tokenops status` byte for byte
// against a canned daemon, so moving its state derivation into
// internal/capability/state cannot change what an operator reads. The
// config names a retention source that never wrote an event, and the
// daemon reports dropped rows, so both warning paths are exercised.
func TestStatusOutputCharacterization(t *testing.T) {
	dir := t.TempDir()
	dbPath := seedFixedSpendDB(t)
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "storage:\n  enabled: true\n  path: " + dbPath + "\n" +
		"retention:\n  keep_by_source:\n    no-such-source: 30d\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor := cursorNotConnected()
	cases := []struct {
		golden string
		ready  fakeResponse
		health string
		json   bool
		conns  []connections.Connection
	}{
		{"status/ready.txt", fakeResponse{200, `{"status":"ready"}`}, `{"status":"ok"}`, false, nil},
		{"status/dropped.json", fakeResponse{200, `{"status":"ready"}`}, `{"status":"ok","dropped_events":12}`, true, nil},
		{"status/not_configured.txt", fakeResponse{503, `{"status":"not_configured"}`}, `{"status":"ok"}`, false, nil},
		{"status/not_ready.json", fakeResponse{503, `{"status":"not_ready"}`}, `{"status":"ok"}`, true, nil},
		// Cursor in use, its plan not read: listed with the command.
		{"status/not_connected.txt", fakeResponse{200, `{"status":"ready"}`}, `{"status":"ok"}`, false, cursor},
		{"status/not_connected.json", fakeResponse{200, `{"status":"ready"}`}, `{"status":"ok"}`, true, cursor},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			prev, prevSources, prevConns := statusClient, statusSourcesFetch, statusConnectionsFetch
			t.Cleanup(func() { SetStatusClient(prev); statusSourcesFetch, statusConnectionsFetch = prevSources, prevConns })
			// The daemon's routes are answered here, never by a daemon
			// running on this machine.
			statusSourcesFetch = func(string) (mcp.DaemonSources, error) { return mcp.DaemonSources{}, errors.New("not asked") }
			statusConnectionsFetch = func(string) ([]connections.Connection, error) { return tc.conns, nil }
			SetStatusClient(&fakeDoer{responses: map[string]fakeResponse{
				"/healthz": {status: 200, body: tc.health},
				"/readyz":  tc.ready,
				"/version": {status: 200, body: `{"version":"dev"}`},
			}})
			args := []string{"--config", cfgPath, "status", "--addr", "127.0.0.1:7878"}
			if tc.json {
				args = append(args, "--json")
			}
			out, err := executeRoot(t, args...)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}
