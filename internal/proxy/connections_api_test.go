package proxy

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var updateConnectionsGolden = flag.Bool("update-connections-golden", false, "rewrite testdata/connections_glance.golden")

// connectionsNow is the clock the golden's connections are computed for.
var connectionsNow = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// cursorTurns are two of Cursor's turns from its stop hook in the last
// hour, as the event log holds them.
func cursorTurns(now time.Time) []*eventschema.Envelope {
	var out []*eventschema.Envelope
	for _, ago := range []time.Duration{time.Hour, 2 * time.Hour} {
		out = append(out, &eventschema.Envelope{Source: "cursor-hook", Timestamp: now.Add(-ago), Type: eventschema.EventTypePrompt,
			Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderCursor, InputTokens: 100}})
	}
	return out
}

// connectionsServer serves a glance over Cursor's turns, with connections
// computed as the daemon computes them: the configuration's dismissals
// read from path at each computation, Cursor's app on this machine.
func connectionsServer(t *testing.T, path string, seen *map[string]plans.ProviderTurns) *Server {
	t.Helper()
	now := time.Now().UTC()
	fn := func(g headroom.Glance, health []state.SourceReport, _ time.Time) []connections.Connection {
		*seen = g.Turns
		cfg, err := config.ReadMutable(path)
		if err != nil {
			t.Errorf("read config: %v", err)
		}
		// The golden pins the shape for a fixed clock: the same turns,
		// counted at connectionsNow.
		turns := map[string]plans.ProviderTurns{"cursor": {Turns: 2, LastAt: connectionsNow.Add(-time.Hour)}}
		return connections.Compute(connections.Inputs{Config: &cfg, Turns: turns, Health: health, Now: connectionsNow,
			Installed: map[string][]string{"cursor": {"/Applications/Cursor.app"}}})
	}
	s, f := newActionServer(t, allowAll{})
	if err := os.WriteFile(path, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.path = path
	for _, o := range []Option{
		WithPlans(func() headroom.Deps { return headroom.Deps{Reader: planEvents{cursorTurns(now)}} }),
		WithConnections(fn),
		WithSourceFreshness(func() []state.SourceReport { return nil }),
		WithActions(func() ActionDeps { return ActionDeps{ConfigPath: path} }),
	} {
		o(s)
	}
	return s
}

// The glance carries the connections, computed from the turns it read: a
// tool in use whose plan is not read, with the one thing to do. GET
// /api/connections serves the same list.
func TestGlanceCarriesTheConnections(t *testing.T) {
	var seen map[string]plans.ProviderTurns
	s := connectionsServer(t, filepath.Join(t.TempDir(), "config.yaml"), &seen)
	var glance headroom.GlancePayload
	if code := getAPI(t, s, "/api/glance", &glance); code != http.StatusOK {
		t.Fatalf("GET /api/glance = %d", code)
	}
	if seen["cursor"].Turns != 2 {
		t.Errorf("the glance's turns = %+v, want Cursor's two", seen)
	}
	got, err := json.MarshalIndent(map[string]any{"connections": glance.Connections}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "connections_glance.golden")
	if *updateConnectionsGolden {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update-connections-golden): %v", err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Errorf("glance connections:\n%s\nwant:\n%s", got, want)
	}

	var report ConnectionsReport
	if code := getAPI(t, s, "/api/connections", &report); code != http.StatusOK || len(report.Connections) != 1 ||
		report.Connections[0].State != connections.NotConnected || report.ComputedAt.IsZero() {
		t.Errorf("GET /api/connections = %d %+v", code, report)
	}
}

// "Don't show" records the dismissal in the config and the next glance
// says it, without a restart.
func TestDismissConnection(t *testing.T) {
	var seen map[string]plans.ProviderTurns
	path := filepath.Join(t.TempDir(), "config.yaml")
	s := connectionsServer(t, path, &seen)
	var before ConnectionsReport
	getAPI(t, s, "/api/connections", &before)

	rec := post(s, "/api/connections/dismiss", "application/json", `{"provider":"cursor"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "dismissed:\n        - cursor") && !strings.Contains(string(b), "dismissed:\n    - cursor") {
		t.Errorf("config after dismiss:\n%s", b)
	}
	s.glances.waitIdle()
	getAPI(t, s, "/api/connections", &before) // starts the computation the dismissal asked for
	s.glances.waitIdle()
	var after ConnectionsReport
	getAPI(t, s, "/api/connections", &after)
	if len(after.Connections) != 1 || after.Connections[0].State != connections.Dismissed {
		t.Errorf("after dismiss = %+v", after.Connections)
	}

	if rec := post(s, "/api/connections/dismiss", "application/json", `{"provider":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown provider = %d %s, want 400", rec.Code, rec.Body)
	}
}
