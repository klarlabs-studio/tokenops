package mcp

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// healthyDaemon stands in for a daemon's listener: /healthz answers 200.
func healthyDaemon(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok","dropped_events":3}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deadURL is an address nothing listens on: a server started and closed.
func deadURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

// isolateHint points the hint at a per-test data dir and returns it.
func isolateHint(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	return dir
}

func writeHint(t *testing.T, dataDir, url string) {
	t.Helper()
	hintDir := filepath.Join(dataDir, "tokenops")
	if err := os.MkdirAll(hintDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"url":"` + url + `","addr":"x","pid":1}`
	if err := os.WriteFile(filepath.Join(hintDir, "daemon.url"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The hint file vanished on the operator's machine while the daemon kept
// running, and status reported "no ingestion daemon is reachable" for a
// healthy one. The configured listen address is where the daemon is; ask
// it before concluding it is gone.
func TestProbeDaemonFindsDaemonWithoutHint(t *testing.T) {
	isolateHint(t)
	d := healthyDaemon(t)

	got := ProbeDaemonAt(d.URL)
	if !got.Alive || got.URL != d.URL || got.Dropped != 3 {
		t.Errorf("report = %+v; want alive at %s with 3 dropped", got, d.URL)
	}
}

// A hint left by a dead daemon is not the last word either: a new daemon on
// the configured address is still the daemon.
func TestProbeDaemonFallsBackWhenHintIsStale(t *testing.T) {
	dir := isolateHint(t)
	writeHint(t, dir, deadURL(t))
	d := healthyDaemon(t)

	if got := ProbeDaemonAt(d.URL); !got.Alive || got.URL != d.URL {
		t.Errorf("report = %+v; want alive at %s", got, d.URL)
	}
}

func TestProbeDaemonPrefersLiveHint(t *testing.T) {
	dir := isolateHint(t)
	hinted := healthyDaemon(t)
	writeHint(t, dir, hinted.URL)

	if got := ProbeDaemonAt(deadURL(t)); !got.Alive || got.URL != hinted.URL {
		t.Errorf("report = %+v; want alive at the hinted %s", got, hinted.URL)
	}
}

func TestProbeDaemonAbsentWhenNothingAnswers(t *testing.T) {
	isolateHint(t)
	if got := ProbeDaemonAt(deadURL(t)); got.Alive {
		t.Errorf("report = %+v; want not alive", got)
	}
	if got := ProbeDaemonAt(""); got.Alive {
		t.Errorf("report with no fallback = %+v; want not alive", got)
	}
}

func TestConfiguredDaemonURL(t *testing.T) {
	cases := []struct {
		listen string
		tls    bool
		want   string
	}{
		{"127.0.0.1:7878", false, "http://127.0.0.1:7878"},
		{"0.0.0.0:7878", false, "http://127.0.0.1:7878"},
		{":7878", false, "http://127.0.0.1:7878"},
		{"[::]:7878", true, "https://127.0.0.1:7878"},
		{"", false, ""},
		{"not-an-address", false, ""},
	}
	for _, c := range cases {
		cfg := config.Default()
		cfg.Listen, cfg.TLS.Enabled = c.listen, c.tls
		if got := ConfiguredDaemonURL(cfg); got != c.want {
			t.Errorf("ConfiguredDaemonURL(%q, tls=%v) = %q; want %q", c.listen, c.tls, got, c.want)
		}
	}
}

// modeFixture registers the mode tools with every side effect faked.
type modeFixture struct {
	srv     *Server
	path    string
	spawned bool
	applied int
}

func newModeFixture(t *testing.T, d ModeDeps) *modeFixture {
	t.Helper()
	f := &modeFixture{path: filepath.Join(t.TempDir(), "config.yaml")}
	if err := config.WriteMutable(f.path, config.Default()); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	d.ConfigPath = f.path
	d.StartDaemon = func(string) (int, string, error) { f.spawned = true; return 4242, "/dev/null", nil }
	d.ApplyConfig = func() string { f.applied++; return "restarted the daemon; the change is live" }
	f.srv = NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterModeTools(f.srv, d); err != nil {
		t.Fatalf("RegisterModeTools: %v", err)
	}
	return f
}

// On a supervised machine the daemon belongs to launchd/systemd. Spawning a
// detached `tokenops start` beside it runs two daemons; the unit restart is
// how a config change reaches the one that should be running — even when
// no probe finds it, since the restart also brings a stopped unit back.
func TestModeActiveNeverSpawnsUnderASupervisor(t *testing.T) {
	isolateHint(t)
	f := newModeFixture(t, ModeDeps{
		DaemonURL:     deadURL(t),
		UnitInstalled: func() bool { return true },
	})

	out := execTool(t, f.srv, "tokenops_mode", map[string]any{"set": "active"})
	if f.spawned {
		t.Error("spawned a detached daemon although a supervisor unit is installed")
	}
	if f.applied != 1 {
		t.Errorf("ApplyConfig called %d times; want 1", f.applied)
	}
	if !strings.Contains(out, "change is live") {
		t.Errorf("response does not carry the restart outcome: %s", out)
	}
}

// A running daemon read its config at boot. Telling the operator to run
// `tokenops start` to apply the change started a second daemon; the change
// goes through the same restart hook every other config tool uses.
func TestModeActiveAppliesToRunningDaemon(t *testing.T) {
	isolateHint(t)
	d := healthyDaemon(t)
	f := newModeFixture(t, ModeDeps{DaemonURL: d.URL, UnitInstalled: func() bool { return false }})

	out := execTool(t, f.srv, "tokenops_mode", map[string]any{"set": "active"})
	if f.spawned {
		t.Error("spawned a second daemon beside a running one")
	}
	if f.applied != 1 {
		t.Errorf("ApplyConfig called %d times; want 1", f.applied)
	}
	if strings.Contains(out, "tokenops start") {
		t.Errorf("response still tells the operator to run `tokenops start`: %s", out)
	}
}

// The spawn is the last resort: no unit, and nothing answers on either the
// hint or the configured address.
func TestModeActiveSpawnsOnlyWhenNothingRuns(t *testing.T) {
	isolateHint(t)
	f := newModeFixture(t, ModeDeps{DaemonURL: deadURL(t), UnitInstalled: func() bool { return false }})

	out := execTool(t, f.srv, "tokenops_mode", map[string]any{"set": "active"})
	if !f.spawned {
		t.Errorf("no daemon spawned although none runs and none is supervised: %s", out)
	}
	if f.applied != 0 {
		t.Errorf("ApplyConfig called %d times for a daemon that was just started with the new config", f.applied)
	}
}

// The mode tool reads presence the same way status does, so a missing hint
// no longer makes it spawn a duplicate beside a healthy daemon.
func TestModeActiveFindsDaemonWithoutHint(t *testing.T) {
	isolateHint(t)
	d := healthyDaemon(t)
	f := newModeFixture(t, ModeDeps{DaemonURL: d.URL})

	execTool(t, f.srv, "tokenops_mode", map[string]any{"set": "active"})
	if f.spawned {
		t.Error("spawned a duplicate daemon because the URL hint was missing")
	}
}

// A refused mode must leave the config untouched and say what is allowed.
func TestModeRejectsUnknownMode(t *testing.T) {
	isolateHint(t)
	f := newModeFixture(t, ModeDeps{})
	before, _ := os.ReadFile(f.path)

	if err := execToolErr(t, f.srv, "tokenops_mode", map[string]any{"set": "turbo"}); err == nil {
		t.Fatal("mode turbo accepted")
	}
	after, _ := os.ReadFile(f.path)
	if string(before) != string(after) {
		t.Error("refused mode still rewrote the config")
	}
	if f.spawned || f.applied != 0 {
		t.Error("refused mode still touched the daemon")
	}
}
