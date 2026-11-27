package proxy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/internal/infra/keychain"

	_ "modernc.org/sqlite"
)

const apiCursorToken = "eyJ-CURSOR-API-SECRET"

// connectFixture is a daemon whose connect routes read Cursor's sign-in
// from a temporary home and ask a fake vendor: no real file, Keychain or
// network is touched.
type connectFixture struct {
	s      *Server
	path   string
	db     string
	logs   *bytes.Buffer
	mu     sync.Mutex
	audits []map[string]any
	// targets are the audit records' targets.
	targets  []string
	restarts int
	asked    []string
	refuse   error
}

func newConnectFixture(t *testing.T, auth DashAuth) *connectFixture {
	t.Helper()
	f := &connectFixture{path: filepath.Join(t.TempDir(), "config.yaml"), logs: &bytes.Buffer{}}
	if err := os.WriteFile(f.path, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	// Nothing reaches the operator's own home or config (config is XDG).
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(k, filepath.Join(home, "env", k))
	}
	f.db = filepath.Join(home, "Library/Application Support/Cursor/User/globalStorage/state.vscdb")
	writeCursorDB(t, f.db, apiCursorToken)
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	env := applogin.Env{Home: home, Getenv: func(string) string { return "" },
		Keychain: func(i keychain.Item) (string, error) {
			t.Errorf("read the Keychain item %s", i)
			return "", keychain.ErrNotFound
		},
		Prompt: func(_ context.Context, i keychain.Item) (string, error) {
			t.Errorf("asked macOS for the Keychain item %s", i)
			return "", keychain.ErrDenied
		}}
	verify := func(ctx context.Context, id string, a providersetup.AppLogin, env applogin.Env) ([]string, error) {
		f.mu.Lock()
		f.asked = append(f.asked, id)
		refuse := f.refuse
		f.mu.Unlock()
		if refuse != nil {
			return nil, refuse
		}
		return []string{"weekly pool: 37% used"}, nil
	}
	opts := []Option{
		WithPlans(func() headroom.Deps { return headroom.Deps{Reader: planEvents{}} }),
		WithConnections(func(headroom.Glance, []state.SourceReport, time.Time) []connections.Connection { return nil }),
		WithActions(func() ActionDeps {
			return ActionDeps{
				ConfigPath: f.path,
				AppLogin:   providersetup.GrantDeps{Env: env, Verify: verify},
				Apply: func() (string, func()) {
					f.mu.Lock()
					f.restarts++
					f.mu.Unlock()
					return "restarting the daemon; the change is live in a few seconds", nil
				},
				Audit: func(_ context.Context, target string, details map[string]any) {
					f.mu.Lock()
					defer f.mu.Unlock()
					f.targets = append(f.targets, target)
					f.audits = append(f.audits, details)
				},
			}
		}),
	}
	if auth != nil {
		opts = append(opts, WithDashAuth(auth))
	}
	f.s = New("127.0.0.1:0", opts...)
	return f
}

// writeCursorDB writes Cursor's state database with its access token in
// the row the descriptor reads.
func writeCursorDB(t *testing.T, path, token string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, q := range []string{
		`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`,
		`INSERT INTO ItemTable VALUES ('cursorAuth/accessToken', '` + token + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

// consent is GET /api/connections/connect for provider.
func (f *connectFixture) consent(t *testing.T, provider string) providersetup.Consent {
	t.Helper()
	var c providersetup.Consent
	if code := getAPI(t, f.s, "/api/connections/connect?provider="+provider, &c); code != http.StatusOK {
		t.Fatalf("GET consent = %d %+v", code, c)
	}
	return c
}

// noToken fails when the token reached an answer, an audit record or a log.
func (f *connectFixture) noToken(t *testing.T, answers ...string) {
	t.Helper()
	f.mu.Lock()
	audits, _ := json.Marshal(f.audits)
	f.mu.Unlock()
	for what, text := range map[string]string{"answer": strings.Join(answers, "\n"), "audit": string(audits), "log": f.logs.String()} {
		if strings.Contains(text, apiCursorToken) {
			t.Errorf("the token reached the %s: %s", what, text)
		}
	}
}

// The consent the panel shows comes from the daemon: the database and its
// column, the one host, the Keychain item macOS asks about, nothing stored.
// Allow grants exactly that, once the vendor accepted it: written, audited,
// the daemon restarted to read it, and the token in no answer, audit or log.
func TestConnectGrantsOnTheVendorsYes(t *testing.T) {
	f := newConnectFixture(t, allowAll{})
	c := f.consent(t, "cursor")
	if c.App != "Cursor" || c.Host != "cursor.com" || c.Keychain != "Cursor Safe Storage" ||
		!strings.Contains(c.What, f.db) || !strings.Contains(c.What, "column value") || c.Stored != providersetup.StoredNothingSecret {
		t.Errorf("consent = %+v", c)
	}

	body, _ := json.Marshal(ConnectConnectionRequest{Provider: "cursor", What: c.What, Host: c.Host})
	rec := post(f.s, "/api/connections/connect", "application/json", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body)
	}
	var got struct {
		providersetup.Connected
		Note string `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Connected — reading Cursor's plan" || got.Note == "" || len(got.Reports) != 1 {
		t.Errorf("answer = %+v", got)
	}
	cfg, err := config.ReadMutable(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := cfg.VendorUsage.Grants["cursor"]; !ok || g.Item != f.db || g.Host != "cursor.com" {
		t.Errorf("grant = %+v", cfg.VendorUsage.Grants)
	}
	if raw, _ := os.ReadFile(f.path); strings.Contains(string(raw), apiCursorToken) {
		t.Error("the config holds the token")
	}
	f.mu.Lock()
	targets, restarts, asked := strings.Join(f.targets, ","), f.restarts, strings.Join(f.asked, ",")
	f.mu.Unlock()
	if targets != "connection_connect" || restarts != 1 || asked != "cursor" {
		t.Errorf("audited %q, restarted %d, asked the vendor for %q", targets, restarts, asked)
	}
	f.noToken(t, rec.Body.String())
}

// A refusal or an expired token writes nothing, audits nothing and says
// in plain words what to do; a request without the consent shown, or for
// something else than was shown, reads nothing at all.
func TestConnectWritesNothingUnlessGranted(t *testing.T) {
	tests := []struct {
		name   string
		refuse error
		body   func(c providersetup.Consent) ConnectConnectionRequest
		code   int
		says   string
		asked  bool
	}{
		{name: "expired", refuse: fmt.Errorf("%w (401)", providersetup.ErrRefused), code: http.StatusUnprocessableEntity,
			says: "Sign in to Cursor, then try again", asked: true},
		{name: "vendor down", refuse: fmt.Errorf("cursor.com /api/usage-summary: status 503"), code: http.StatusBadGateway,
			says: "Couldn't read Cursor's plan", asked: true},
		{name: "not shown", body: func(providersetup.Consent) ConnectConnectionRequest {
			return ConnectConnectionRequest{Provider: "cursor"}
		}, code: http.StatusBadRequest, says: "show the operator what is read first"},
		{name: "changed", body: func(c providersetup.Consent) ConnectConnectionRequest {
			return ConnectConnectionRequest{Provider: "cursor", What: "the file /etc/passwd", Host: c.Host}
		}, code: http.StatusConflict, says: "changed since you were shown it"},
		{name: "not an app login", body: func(c providersetup.Consent) ConnectConnectionRequest {
			return ConnectConnectionRequest{Provider: "openrouter", What: c.What, Host: c.Host}
		}, code: http.StatusBadRequest, says: "not read with another application's sign-in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newConnectFixture(t, allowAll{})
			f.refuse = tt.refuse
			c := f.consent(t, "cursor")
			req := ConnectConnectionRequest{Provider: "cursor", What: c.What, Host: c.Host}
			if tt.body != nil {
				req = tt.body(c)
			}
			before, _ := os.ReadFile(f.path)
			body, _ := json.Marshal(req)
			rec := post(f.s, "/api/connections/connect", "application/json", string(body))
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.says) {
				t.Errorf("connect = %d %s, want %d saying %q", rec.Code, rec.Body, tt.code, tt.says)
			}
			if after, _ := os.ReadFile(f.path); !bytes.Equal(after, before) {
				t.Errorf("wrote the config:\n%s", after)
			}
			f.mu.Lock()
			targets, restarts, asked := f.targets, f.restarts, f.asked
			f.mu.Unlock()
			if len(targets) != 0 || restarts != 0 || (len(asked) > 0) != tt.asked {
				t.Errorf("audited %v, restarted %d, asked the vendor %v", targets, restarts, asked)
			}
			f.noToken(t, rec.Body.String())
		})
	}
}

// The routes are served only behind the API token, like every write.
func TestConnectNeedsTheAPIToken(t *testing.T) {
	f := newConnectFixture(t, nil)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if f.s.ServesAPI(m, "/api/connections/connect") {
			t.Errorf("%s /api/connections/connect is mounted without API auth", m)
		}
	}
	if !newConnectFixture(t, allowAll{}).s.ServesAPI(http.MethodPost, "/api/connections/connect") {
		t.Error("POST /api/connections/connect is not mounted behind the token")
	}
}

// A consent for a sign-in that is not on this machine says so.
func TestConnectConsentWithoutASignIn(t *testing.T) {
	f := newConnectFixture(t, allowAll{})
	if err := os.Remove(f.db); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	f.s.apiMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/connections/connect?provider=cursor", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Sign in to Cursor, then try again") {
		t.Errorf("consent = %d %s", rec.Code, rec.Body)
	}
}
