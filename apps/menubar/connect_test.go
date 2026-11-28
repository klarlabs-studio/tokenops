package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.klarlabs.de/vitra"
	"go.klarlabs.de/vitra/domain"
)

var updateConnect = flag.Bool("update-connect", false, "rewrite testdata/connect_*.golden")

// cursorAppGlance is a daemon's glance where Cursor is in use, its own
// sign-in is on this Mac and granting it is the one thing to do.
const cursorAppGlance = `{"plan_headroom":{"reports":[{"provider":"anthropic"}]},"connections":[
 {"provider":"cursor","display":"Cursor","state":"not_connected","summary":"Cursor — in use, plan not read",
  "evidence":[{"kind":"turns","detail":"41 turns in the last 7 days","turns":41}],
  "source":"cursor-app","action":{"source":"cursor-app","command":"tokenops vendor-usage setup cursor --use-app-login",
  "needs":"Cursor's own sign-in on this Mac; asks first",
  "docs":"https://github.com/klarlabs-studio/tokenops/blob/main/docs/reference/cli.md#connect-a-tool-tokenops-found",
  "dismiss":"tokenops vendor-usage dismiss cursor","grant":true}},
 {"provider":"amp","display":"Amp","state":"not_connected","summary":"Amp — installed, plan not read",
  "action":{"command":"tokenops vendor-usage setup amp","needs":"an Amp API key"}}
]}`

// cursorConsent is the daemon's GET /api/connections/connect for Cursor.
const cursorConsent = `{"provider":"cursor","name":"Cursor","app":"Cursor",
 "what":"the database /Users/me/Library/Application Support/Cursor/User/globalStorage/state.vscdb, column value (SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken' LIMIT 1); opened with the Keychain item \"Cursor Safe Storage\" (account \"Cursor Key\"), read to decrypt it",
 "host":"cursor.com","keychain":"Cursor Safe Storage","stored":"nothing secret; only this grant, in your config"}`

// connectDaemon answers the connect routes and /version as a daemon would.
type connectDaemon struct {
	mu sync.Mutex
	// refuse is the POST's error answer and status; empty grants.
	refuse     string
	refuseCode int
	// version is /version's body.
	version string
	calls   []string
	posted  []map[string]any
}

func (f *connectDaemon) start(t *testing.T) *daemon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.URL.Path == "/version":
			_, _ = w.Write([]byte(f.version))
		case r.Method == http.MethodGet && r.URL.Path == "/api/connections/connect":
			if r.URL.Query().Get("provider") != "cursor" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"No sign-in was found on this Mac."}`))
				return
			}
			_, _ = w.Write([]byte(cursorConsent))
		case r.Method == http.MethodPost && r.URL.Path == "/api/connections/connect":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.posted = append(f.posted, in)
			if f.refuse != "" {
				w.WriteHeader(f.refuseCode)
				b, _ := json.Marshal(map[string]string{"error": f.refuse})
				_, _ = w.Write(b)
				return
			}
			_, _ = w.Write([]byte(`{"provider":"cursor","app":"Cursor","summary":"Connected — reading Cursor's plan","reports":["weekly pool: 37% used"],"note":"restarting the daemon; the change is live in a few seconds"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	d := &daemon{urlFile: filepath.Join(t.TempDir(), "daemon.url"), hc: srv.Client()}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

// connectMenubar is a menu bar that last read cursorAppGlance from fd.
func connectMenubar(t *testing.T, fd *connectDaemon) *menubar {
	t.Helper()
	prev := connectAgain
	connectAgain = nil
	t.Cleanup(func() { connectAgain = prev })
	m := &menubar{daemon: fd.start(t)}
	m.last.Glance = json.RawMessage(cursorAppGlance)
	m.last.Connections = connectionRows(json.RawMessage(cursorAppGlance))
	return m
}

// golden compares v, as JSON, with testdata/name.
func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *updateConnect {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update-connect): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s:\n%s\nwant:\n%s", name, got, want)
	}
}

// rowOf is provider's row in v.
func rowOf(t *testing.T, v view, provider string) connectionRow {
	t.Helper()
	for _, r := range v.Connections {
		if r.Provider == provider {
			return r
		}
	}
	t.Fatalf("no row for %s in %+v", provider, v.Connections)
	return connectionRow{}
}

// A grant is marked on its row; a row that needs something typed is not.
func TestConnectionRowsMarkAGrant(t *testing.T) {
	rows := connectionRows(json.RawMessage(cursorAppGlance))
	if len(rows) != 2 || !rows[0].Grant || rows[1].Grant {
		t.Errorf("rows = %+v", rows)
	}
}

// Connect… on a grant row shows what the CLI shows before y/N, from the
// daemon: what is read, the one host, that nothing secret is stored, and
// that macOS will ask about the Keychain item. Nothing is granted yet.
func TestConnectShowsTheConsent(t *testing.T) {
	fd := &connectDaemon{}
	m := connectMenubar(t, fd)
	if err := m.openConsent(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	r := rowOf(t, m.current(), "cursor")
	golden(t, "connect_consent.golden", r)
	if len(fd.posted) != 0 {
		t.Errorf("posted %v before Allow", fd.posted)
	}
	if err := m.openConsent(context.Background(), connectionInput{Provider: "amp"}); !errors.Is(err, errBadConnection) {
		t.Errorf("consent for a typed-key row = %v, want refused", err)
	}
}

// Allow grants exactly what was shown, through the daemon; the row says
// it is connected, and the next reads turn it into the plan.
func TestAllowConnects(t *testing.T) {
	fd := &connectDaemon{}
	m := connectMenubar(t, fd)
	if err := m.connect(context.Background(), connectionInput{Provider: "cursor"}); !errors.Is(err, errBadConnection) {
		t.Errorf("Allow before the consent was shown = %v, want refused", err)
	}
	if err := m.openConsent(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	if err := m.connect(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	var c struct{ What, Host string }
	_ = json.Unmarshal([]byte(cursorConsent), &c)
	if len(fd.posted) != 1 || fd.posted[0]["provider"] != "cursor" || fd.posted[0]["what"] != c.What || fd.posted[0]["host"] != c.Host {
		t.Fatalf("posted %v", fd.posted)
	}
	golden(t, "connect_allowed.golden", rowOf(t, m.current(), "cursor"))
}

// A refusal says the daemon's words in place: no alert, nothing granted.
func TestAllowSaysWhyNot(t *testing.T) {
	tests := []struct {
		name, refuse string
		code         int
		says         string
	}{
		{name: "expired", code: http.StatusUnprocessableEntity,
			refuse: "Cursor did not accept Cursor's sign-in; it may have expired. Sign in to Cursor, then try again.",
			says:   "Sign in to Cursor, then try again."},
		{name: "keychain", code: http.StatusForbidden,
			refuse: `macOS did not allow TokenOps to use "Cursor Safe Storage", so nothing was read. Try again and choose Allow.`,
			says:   `macOS did not allow TokenOps to use "Cursor Safe Storage"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := &connectDaemon{refuse: tt.refuse, refuseCode: tt.code}
			m := connectMenubar(t, fd)
			if err := m.openConsent(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
				t.Fatal(err)
			}
			if err := m.connect(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
				t.Fatal(err)
			}
			r := rowOf(t, m.current(), "cursor")
			if r.Result == nil || r.Result.Connected || !strings.Contains(r.Result.Text, tt.says) {
				t.Errorf("result = %+v", r.Result)
			}
			if r.Consent == nil {
				t.Error("the consent is gone after a refusal; Allow cannot be tried again")
			}
		})
	}
	fd := &connectDaemon{}
	m := connectMenubar(t, fd)
	if err := m.openConsent(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.daemon.urlFile); err != nil {
		t.Fatal(err)
	}
	if err := m.connect(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, m.current(), "cursor"); r.Result == nil || r.Result.Connected || !strings.Contains(r.Result.Text, "nothing was granted") {
		t.Errorf("no daemon: result = %+v", r.Result)
	}
}

// Connect… and Allow need connections.grant, which only the panel holds.
func TestConnectCommandsNeedTheGrantPermission(t *testing.T) {
	rt, err := vitra.New(vitra.Config{AppID: appID})
	if err != nil {
		t.Fatal(err)
	}
	fd := &connectDaemon{}
	m := &menubar{rt: rt, daemon: fd.start(t)}
	if err := m.register(); err != nil {
		t.Fatal(err)
	}
	for _, w := range []domain.WindowID{panelWindow, "other"} {
		if _, err := rt.OpenWindow(context.Background(), w, domain.OriginPackagedLocal); err != nil {
			t.Fatal(err)
		}
	}
	panel, _ := domain.NewCaller(panelWindow, domain.OriginPackagedLocal)
	other, _ := domain.NewCaller("other", domain.OriginPackagedLocal)
	for _, command := range []string{"connection.consent", "connection.connect"} {
		_, err := rt.Invoke(context.Background(), domain.InvocationRequest{Caller: panel, Command: domain.CommandName(command),
			Input: map[string]any{"provider": "cursor"}})
		var denied *domain.ErrDenied
		if errors.As(err, &denied) || !errors.Is(err, errBadConnection) {
			t.Errorf("panel %s = %v, want the handler's check (nothing listed yet)", command, err)
		}
		_, err = rt.Invoke(context.Background(), domain.InvocationRequest{Caller: other, Command: domain.CommandName(command),
			Input: map[string]any{"provider": "cursor"}})
		if !errors.As(err, &denied) {
			t.Errorf("another window's %s = %v, want a denial", command, err)
		}
	}
	if len(fd.posted) != 0 {
		t.Fatalf("posted %v", fd.posted)
	}
}

// The footer names the running daemon's version, the app's own when it
// differs, and while an upgrade waits for the daemon to switch, what it is
// updating to rather than the number about to go stale.
func TestVersionLine(t *testing.T) {
	tests := []struct {
		name string
		v    versionInfo
		want string
	}{
		{name: "same", v: versionInfo{Daemon: "0.127.0", App: "0.127.0"}, want: "TokenOps 0.127.0"},
		{name: "app differs", v: versionInfo{Daemon: "0.127.0", App: "0.126.2"}, want: "TokenOps 0.127.0 · menu bar 0.126.2"},
		{name: "updating", v: versionInfo{Daemon: "0.127.0", App: "0.128.0", Updating: true, Installed: "0.128.0", RestartsItself: true},
			want: "TokenOps updating to 0.128.0"},
		{name: "updating, app behind", v: versionInfo{Daemon: "0.127.0", App: "0.127.0", Updating: true, Installed: "0.128.0", RestartsItself: true},
			want: "TokenOps updating to 0.128.0 · menu bar 0.127.0"},
		{name: "updating to an unknown version", v: versionInfo{Daemon: "0.127.0", App: "0.127.0", Updating: true, RestartsItself: true},
			want: "TokenOps updating…"},
		{name: "installed, daemon run by hand", v: versionInfo{Daemon: "0.127.0", App: "0.128.0", Updating: true, Installed: "0.128.0"},
			want: "TokenOps 0.127.0 · 0.128.0 installed; restart the daemon to use it"},
		{name: "a build from a checkout", v: versionInfo{Daemon: "dev", App: "dev"}, want: "TokenOps dev"},
		{name: "no daemon", v: versionInfo{App: "0.127.0"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.line(); got != tt.want {
				t.Errorf("line = %q, want %q", got, tt.want)
			}
		})
	}
}

// Each read asks the daemon's /version and carries the footer's line.
func TestReadCarriesTheVersion(t *testing.T) {
	prev := appVersion
	// The cask upgraded the app already; the daemon switches next.
	appVersion = "0.128.0"
	t.Cleanup(func() { appVersion = prev })
	fd := &connectDaemon{version: `{"version":"0.127.0","commit":"abc","date":"2026-11-27","updating":true,"installed":"0.128.0","restarts_itself":true}`}
	m := &menubar{daemon: fd.start(t)}
	v := m.versionOf(context.Background())
	if v.line() != "TokenOps updating to 0.128.0" {
		t.Errorf("version = %+v, line %q", v, v.line())
	}
}

// The panel keeps an expanded row in view, shows the consent with Allow
// and Not now, and the version in the footer; the details area shows that
// it scrolls.
func TestPanelRendersConnectAndVersion(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(frontendFS, "frontend/"+name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	js, css, html := read("panel.js"), read("style.css"), read("index.html")
	for _, want := range []string{
		`"connection.consent"`, `"connection.connect"`, `"Allow"`, `"Not now"`, "What is read", "Sent only to", "Stored",
		"scrollIntoView", "keepScroll", "r.consent.keychain", "r.result", `"version"`, "v.version_line", "Copied",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("panel.js lacks %s", want)
		}
	}
	if !strings.Contains(html, `id="version"`) {
		t.Error("index.html has no version line in the footer")
	}
	if strings.Contains(css, ".detail::-webkit-scrollbar { width: 0; }") {
		t.Error("the details area hides its scrollbar: what is below the fold looks absent")
	}
	for _, want := range []string{".version {", ".connection-consent", ".connection-result"} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css lacks %s", want)
		}
	}
}
