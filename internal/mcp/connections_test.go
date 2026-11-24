package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func cursorDetected() []connections.Connection {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	return connections.Compute(connections.Inputs{Now: now,
		Turns:     map[string]plans.ProviderTurns{"cursor": {Turns: 41, LastAt: now.Add(-2 * time.Hour)}},
		Installed: map[string][]string{"cursor": {"/Applications/Cursor.app"}}})
}

// The glance an agent reads carries the daemon's connections, and its text
// tells the agent to hand the command to the operator.
func TestGlanceCarriesConnectionsToTheAgent(t *testing.T) {
	var g headroom.Glance
	p := g.Payload()
	p.Connections = cursorDetected()
	d := PlanDeps{Daemon: func(_ context.Context, path string, out any) error {
		b, _ := json.Marshal(p)
		return json.Unmarshal(b, out)
	}}
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterPlanTools(srv, d); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_resource_glance", nil)
	for _, want := range []string{
		"### In use, plan not read",
		"never run it yourself",
		"- Cursor — in use, plan not read: `TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor`",
		`"state": "not_connected"`,
		`"dismiss": "tokenops vendor-usage dismiss cursor"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("glance lacks %q:\n%s", want, out)
		}
	}
}

// tokenops_status names what is in use and not read, from a live daemon.
func TestStatusNamesWhatIsNotConnected(t *testing.T) {
	d := ControlDeps{
		DaemonProbe: func() DaemonReport { return DaemonReport{Alive: true, URL: "http://daemon"} },
		Connections: func(url string) ([]connections.Connection, error) {
			if url != "http://daemon" {
				t.Errorf("asked %q", url)
			}
			return cursorDetected(), nil
		},
	}
	got := statusInfo(d)
	if len(got.NotConnected) != 1 || got.NotConnected[0].Provider != "cursor" || got.NotConnected[0].Action == nil {
		t.Fatalf("not_connected = %+v", got.NotConnected)
	}
	d.DaemonProbe = func() DaemonReport { return DaemonReport{} }
	if got := statusInfo(d); got.NotConnected != nil {
		t.Errorf("a daemon not reached was asked: %+v", got.NotConnected)
	}
}
