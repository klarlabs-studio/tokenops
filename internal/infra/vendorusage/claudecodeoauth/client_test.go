package claudecodeoauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	oauth "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// A fake credential: no real token appears in these tests.
func credsJSON(token string, expires time.Time, scopes string) []byte {
	return []byte(`{"claudeAiOauth":{"accessToken":"` + token + `","refreshToken":"never-read","expiresAt":` +
		strconv.FormatInt(expires.UnixMilli(), 10) + `,"scopes":` + scopes + `,"subscriptionType":"max"}}`)
}

func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatal("no exit error")
	}
	return err
}

// The Keychain store asks /usr/bin/security for the item's secret and
// tells "not there" apart from "not allowed".
func TestKeychainStore(t *testing.T) {
	var got []string
	ok := KeychainStore{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return credsJSON("tok", now.Add(time.Hour), `["user:profile"]`), nil
	}}
	if c, err := ok.Read(context.Background()); err != nil || c.AccessToken != "tok" {
		t.Fatalf("read = %+v, %v", c, err)
	}
	want := []string{"/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w"}
	if len(got) != len(want) {
		t.Fatalf("command %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command %v", got)
		}
	}
	missing := KeychainStore{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, exitErr(t, 44) }}
	if _, err := missing.Read(context.Background()); !errors.Is(err, oauth.ErrNotSignedIn) {
		t.Errorf("missing item: %v", err)
	}
	denied := KeychainStore{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, exitErr(t, 51) }}
	if _, err := denied.Read(context.Background()); !errors.Is(err, oauth.ErrKeychainDenied) {
		t.Errorf("denied: %v", err)
	}
}

// The file is read before the Keychain, which is only asked when allowed;
// a denial is reported, not hidden behind "not signed in".
func TestStoresAndReadFirst(t *testing.T) {
	home := t.TempDir()
	if s := Stores(home, false, false); len(s) != 1 {
		t.Errorf("keychain listed without being allowed: %v", s)
	}
	denied := KeychainStore{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, exitErr(t, 51) }}
	if _, err := oauth.ReadFirst(context.Background(), []oauth.Store{FileStore{Path: filepath.Join(home, "absent")}, denied}); !errors.Is(err, oauth.ErrKeychainDenied) {
		t.Errorf("denial hidden: %v", err)
	}
	path := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, credsJSON("from-file", now.Add(time.Hour), `["user:profile"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := oauth.ReadFirst(context.Background(), Stores(home, true, false)); err != nil || c.AccessToken != "from-file" {
		t.Errorf("file first: %+v, %v", c, err)
	}
}

const usageBody = `{"five_hour":{"utilization":10,"resets_at":"2026-10-06T15:00:00Z"},
 "seven_day":{"utilization":22,"resets_at":"2026-10-09T23:00:00Z"}}`

// The request carries the token, the beta header and an honest agent; the
// answers map to a reading, an expired sign-in, or a wait.
func TestClient(t *testing.T) {
	var auth, beta, agent string
	status, retry := http.StatusOK, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, beta, agent = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta"), r.Header.Get("User-Agent")
		if r.URL.Path != "/api/oauth/usage" {
			t.Errorf("path %s", r.URL.Path)
		}
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(usageBody))
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, UserAgent: "tokenops/test"}
	u, err := c.Usage(context.Background(), "tok", now)
	if err != nil || !u.HasSignal() || auth != "Bearer tok" || beta != "oauth-2025-04-20" || agent != "tokenops/test" {
		t.Fatalf("usage %+v %v; headers %q %q %q", u, err, auth, beta, agent)
	}
	status = http.StatusUnauthorized
	if _, err := c.Usage(context.Background(), "tok", now); !errors.Is(err, oauth.ErrExpired) {
		t.Errorf("401: %v", err)
	}
	status, retry = http.StatusTooManyRequests, "120"
	_, err = c.Usage(context.Background(), "tok", now)
	var limited *oauth.RateLimitedError
	if !errors.As(err, &limited) || !limited.Until.Equal(now.Add(2*time.Minute)) {
		t.Errorf("429: %v", err)
	}
}

// The daemon's store reads quietly: when macOS would ask, it is a denial
// for now, never a prompt; a missing item is "not signed in".
func TestQuietKeychainStore(t *testing.T) {
	if s := Stores(t.TempDir(), true, false); len(s) != 2 || s[1].(KeychainStore).Prompts() {
		t.Fatalf("stores %v: want the file and a quiet keychain store", s)
	}
	ask := KeychainStore{Quiet: true, QuietRead: func(keychain.Item) (string, error) { return "", keychain.ErrInteractionRequired }}
	if _, err := ask.Read(context.Background()); !errors.Is(err, oauth.ErrKeychainDenied) {
		t.Errorf("would ask: %v, want ErrKeychainDenied", err)
	}
	missing := KeychainStore{Quiet: true, QuietRead: func(keychain.Item) (string, error) { return "", keychain.ErrNotFound }}
	if _, err := missing.Read(context.Background()); !errors.Is(err, oauth.ErrNotSignedIn) {
		t.Errorf("missing: %v, want ErrNotSignedIn", err)
	}
	if !(KeychainStore{}).Prompts() {
		t.Error("a prompting store says it does not prompt")
	}
}
