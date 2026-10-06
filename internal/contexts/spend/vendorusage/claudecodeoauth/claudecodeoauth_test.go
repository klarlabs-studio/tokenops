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
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// A fake credential: no real token appears in these tests.
func credsJSON(token string, expires time.Time, scopes string) []byte {
	return []byte(`{"claudeAiOauth":{"accessToken":"` + token + `","refreshToken":"never-read","expiresAt":` +
		strconv.FormatInt(expires.UnixMilli(), 10) + `,"scopes":` + scopes + `,"subscriptionType":"max"}}`)
}

func TestParse(t *testing.T) {
	c, err := Parse(credsJSON("tok", now.Add(time.Hour), `["user:inference","user:profile"]`))
	if err != nil || c.AccessToken != "tok" || !c.ExpiresAt.Equal(now.Add(time.Hour)) || c.SubscriptionType != "max" {
		t.Fatalf("parse = %+v, %v", c, err)
	}
	if !c.Expired(now.Add(59*time.Minute+30*time.Second)) || c.Expired(now) {
		t.Error("expiry margin")
	}
	if _, err := Parse(credsJSON("tok", now, `["user:inference"]`)); !errors.Is(err, ErrNoUsageScope) {
		t.Errorf("setup-token scope: %v", err)
	}
	if _, err := Parse([]byte(`{"mcpOAuth":{}}`)); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("MCP-only item: %v", err)
	}
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
	if _, err := missing.Read(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("missing item: %v", err)
	}
	denied := KeychainStore{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, exitErr(t, 51) }}
	if _, err := denied.Read(context.Background()); !errors.Is(err, ErrKeychainDenied) {
		t.Errorf("denied: %v", err)
	}
}

// The file is read before the Keychain, which is only asked when allowed;
// a denial is reported, not hidden behind "not signed in".
func TestStoresAndReadFirst(t *testing.T) {
	home := t.TempDir()
	if s := Stores(home, false); len(s) != 1 {
		t.Errorf("keychain listed without being allowed: %v", s)
	}
	denied := KeychainStore{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, exitErr(t, 51) }}
	if _, err := ReadFirst(context.Background(), []Store{FileStore{Path: filepath.Join(home, "absent")}, denied}); !errors.Is(err, ErrKeychainDenied) {
		t.Errorf("denial hidden: %v", err)
	}
	path := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, credsJSON("from-file", now.Add(time.Hour), `["user:profile"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := ReadFirst(context.Background(), Stores(home, true)); err != nil || c.AccessToken != "from-file" {
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
	if _, err := c.Usage(context.Background(), "tok", now); !errors.Is(err, ErrExpired) {
		t.Errorf("401: %v", err)
	}
	status, retry = http.StatusTooManyRequests, "120"
	_, err = c.Usage(context.Background(), "tok", now)
	var limited *RateLimitedError
	if !errors.As(err, &limited) || !limited.Until.Equal(now.Add(2*time.Minute)) {
		t.Errorf("429: %v", err)
	}
}

type recordingBus struct {
	mu  sync.Mutex
	got []*eventschema.Envelope
}

func (b *recordingBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, env)
}

func (b *recordingBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
func (b *recordingBus) DroppedCount() int64       { return 0 }
func (b *recordingBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *recordingBus) Close(time.Duration) error { return nil }

type fakeStore struct {
	reads int
	creds []Credentials
	err   error
}

func (*fakeStore) Name() string { return "fake" }
func (f *fakeStore) Read(context.Context) (Credentials, error) {
	f.reads++
	if f.err != nil {
		return Credentials{}, f.err
	}
	c := f.creds[min(f.reads, len(f.creds))-1]
	return c, nil
}

// A reading is published under this source's tag in the meter's shape,
// once per change; the token is read once and kept while it is valid.
func TestPollerPublishes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(usageBody)) }))
	defer srv.Close()
	bus := &recordingBus{}
	store := &fakeStore{creds: []Credentials{{AccessToken: "tok", ExpiresAt: now.Add(time.Hour)}}}
	p := NewPoller(bus, PollerOptions{Stores: []Store{store}, Client: Client{BaseURL: srv.URL}, Now: func() time.Time { return now }})
	p.Scan(context.Background())
	p.Scan(context.Background())
	if len(bus.got) != 1 || bus.got[0].Source != SourceTag || bus.got[0].Attributes["seven_day_used_pct"] != "22.00" {
		t.Fatalf("published %+v", bus.got)
	}
	if _, ok := bus.got[0].Attributes["org_id"]; ok {
		t.Error("an org id nobody resolved")
	}
	if store.reads != 1 {
		t.Errorf("token read %d times", store.reads)
	}
}

// Refused, the poller re-reads once: Claude Code may have renewed the
// token since. It never refreshes it itself.
func TestPollerRereadsARenewedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer renewed" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(usageBody))
	}))
	defer srv.Close()
	bus := &recordingBus{}
	store := &fakeStore{creds: []Credentials{{AccessToken: "old"}, {AccessToken: "renewed"}}}
	p := NewPoller(bus, PollerOptions{Stores: []Store{store}, Client: Client{BaseURL: srv.URL}, Now: func() time.Time { return now }})
	p.Scan(context.Background())
	if len(bus.got) != 1 || store.reads != 2 {
		t.Errorf("published %d after %d reads", len(bus.got), store.reads)
	}
}

// A declined Keychain is not asked again for hours, and a 429 holds every
// request until the time it names.
func TestPollerBacksOff(t *testing.T) {
	at := now
	denied := KeychainStore{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, exitErr(t, 51) }}
	asks := 0
	counting := KeychainStore{Run: func(ctx context.Context, n string, a ...string) ([]byte, error) {
		asks++
		return denied.Run(ctx, n, a...)
	}}
	p := NewPoller(nil, PollerOptions{Stores: []Store{counting}, Now: func() time.Time { return at }})
	p.Scan(context.Background())
	at = at.Add(time.Hour)
	p.Scan(context.Background())
	if asks != 1 {
		t.Errorf("keychain asked %d times within the backoff", asks)
	}
	at = at.Add(keychainBackoff)
	p.Scan(context.Background())
	if asks != 2 {
		t.Errorf("keychain not asked again after the backoff: %d", asks)
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	at = now
	store := &fakeStore{creds: []Credentials{{AccessToken: "tok"}}}
	p = NewPoller(nil, PollerOptions{Stores: []Store{store}, Client: Client{BaseURL: srv.URL}, Now: func() time.Time { return at }})
	p.Scan(context.Background())
	at = at.Add(5 * time.Minute)
	p.Scan(context.Background())
	at = at.Add(6 * time.Minute)
	p.Scan(context.Background())
	if calls != 2 {
		t.Errorf("endpoint called %d times; the 429 should hold the second", calls)
	}
}
