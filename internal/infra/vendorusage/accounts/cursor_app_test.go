package accounts

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	cursorusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
)

var cursorNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

// cursorJWT is an unsigned access token with the claims Cursor's own client
// reads: the subject and the expiry.
func cursorJWT(t *testing.T, sub string, exp time.Time) string {
	t.Helper()
	enc := base64.RawURLEncoding
	body, err := json.Marshal(map[string]any{"sub": sub, "exp": exp.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(body) + ".sig"
}

// cursorServer answers the two usage endpoints cursor-web reads. It records
// the cookie of every request, and whether the pool endpoint was asked.
func cursorServer(t *testing.T, summary, sand string, status int, cookies *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*cookies = append(*cookies, r.Header.Get("Cookie"))
		switch {
		case status != http.StatusOK:
			w.WriteHeader(status)
		case r.URL.Path == "/api/usage-summary" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(summary))
		case r.URL.Path == "/api/dashboard/get-sand-usage-status" && r.Method == http.MethodPost:
			if sand == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(sand))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const cursorSummary = `{"billingCycleStart":"2026-11-01T00:00:00.000Z","billingCycleEnd":"2026-12-01T00:00:00.000Z",` +
	`"membershipType":"pro","individualUsage":{"plan":{"used":1000,"limit":2000,"totalPercentUsed":50}}}`

func TestCursorAppReadsWeeklyPoolWithCursorsCookie(t *testing.T) {
	token := cursorJWT(t, "auth0|user_01ABC", cursorNow.Add(time.Hour))
	reset := cursorNow.Add(3 * 24 * time.Hour).UTC().Truncate(time.Second)
	sand := `{"isUnified":true,"usagePercent":37.5,"nextResetTimestampUtc":"` + reset.Format(time.RFC3339) + `","cursorPlanName":"Pro"}`
	var cookies []string
	srv := cursorServer(t, cursorSummary, sand, http.StatusOK, &cookies)

	got, err := CursorApp{BaseURL: srv.URL, Now: func() time.Time { return cursorNow }}.ReadAppLogin(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("reading %+v", got)
	}
	if w := got.Windows[0]; w.Name != "week" || !approx(w.UsedPct, 37.5) || !w.ResetsAt.Equal(reset) || w.Duration != 7*24*time.Hour {
		t.Errorf("week window %+v", w)
	}
	want := "WorkosCursorSessionToken=user_01ABC%3A%3A" + token
	for _, c := range cookies {
		if c != want {
			t.Fatalf("cookie %q, want %q", c, want)
		}
	}
	if len(cookies) != 2 {
		t.Errorf("asked %d times, want the summary and the pool", len(cookies))
	}
}

func TestCursorAppFallsBackToTheBillingCycle(t *testing.T) {
	token := cursorJWT(t, "user_01ABC", cursorNow.Add(time.Hour))
	var cookies []string
	srv := cursorServer(t, fixture(t, "cursor"), "", http.StatusOK, &cookies)
	got, err := CursorApp{BaseURL: srv.URL, Now: func() time.Time { return cursorNow }}.ReadAppLogin(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Windows) != 1 {
		t.Fatalf("windows %+v", got.Windows)
	}
	end, _ := time.Parse(time.RFC3339, "2026-12-01T00:00:00Z")
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 50) || !w.ResetsAt.Equal(end) {
		t.Errorf("month window %+v", w)
	}
}

func TestCursorAppRefusesWhatItCannotUse(t *testing.T) {
	live := cursorJWT(t, "user_01ABC", cursorNow.Add(time.Hour))
	expired := cursorJWT(t, "user_01ABC", cursorNow.Add(-time.Hour))
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	reader := CursorApp{BaseURL: srv.URL, Now: func() time.Time { return cursorNow }}

	if _, err := reader.ReadAppLogin(context.Background(), expired); !errors.Is(err, usage.ErrAuth) || !strings.Contains(err.Error(), "sign in to Cursor again") {
		t.Errorf("expired sign-in: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("an expired sign-in was sent to cursor.com")
	}
	if _, err := reader.ReadAppLogin(context.Background(), "opaque"); !errors.Is(err, cursorusage.ErrNotJWT) {
		t.Errorf("opaque token: %v", err)
	}
	if _, err := reader.ReadAppLogin(context.Background(), live); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused session should be ErrAuth: %v", err)
	}
}

// The grant reads exactly the access token from Cursor's state database,
// through the descriptor's own query, from a copy; the reader then reads
// cursor.com with it.
func TestCursorGrantReadsTheStateDatabase(t *testing.T) {
	home := t.TempDir()
	token := cursorJWT(t, "auth0|user_01ABC", cursorNow.Add(time.Hour))
	path := filepath.Join(home, "Library/Application Support/Cursor/User/globalStorage/state.vscdb")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`,
		`INSERT INTO ItemTable VALUES ('cursorAuth/accessToken', '` + token + `')`,
		`INSERT INTO ItemTable VALUES ('cursorAuth/refreshToken', 'never-read')`,
		`INSERT INTO ItemTable VALUES ('cursorAuth/cachedEmail', 'nobody@example.test')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	d, _ := providers.Lookup("cursor")
	s, ok := d.AppLoginSource()
	if !ok || s.Tag != "cursor-app" {
		t.Fatalf("app login source %+v", s)
	}
	env := applogin.Env{Home: home, Getenv: func(string) string { return "" }}
	l, err := applogin.Locate(context.Background(), s.AppLogins, env)
	if err != nil {
		t.Fatal(err)
	}
	if l.Item != path {
		t.Fatalf("located %q, want %q", l.Item, path)
	}
	tok, err := applogin.Read(context.Background(), l, env)
	if err != nil || tok != token {
		t.Fatalf("token read %v", err)
	}
}

// A grant covers exactly what was shown: a descriptor that would read
// another column or send the token to another host does not inherit it.
func TestCursorGrantEndsWithTheDescriptor(t *testing.T) {
	d, _ := providers.Lookup("cursor")
	s, ok := d.AppLoginSource()
	if !ok {
		t.Fatal("no app-login source")
	}
	item := "/Users/me/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
	env := applogin.Env{Home: "/Users/me", Getenv: func(string) string { return "" }}
	if _, ok := applogin.Granted(s.AppLogins, "sqlite", item, []string{"value"}, "cursor.com", false, env); !ok {
		t.Fatal("the grant the descriptor shows is not covered by it")
	}
	if _, ok := applogin.Granted(s.AppLogins, "sqlite", item, []string{"email"}, "cursor.com", false, env); ok {
		t.Error("a grant covers another column")
	}
	if _, ok := applogin.Granted(s.AppLogins, "sqlite", item, []string{"value"}, "example.com", false, env); ok {
		t.Error("a grant covers another host")
	}
}
