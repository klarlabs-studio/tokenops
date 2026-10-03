package fireworks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAPI serves the documented endpoints. userLimits is the body of the
// member's own usageLimits, "" for a 404.
func fakeAPI(t *testing.T, userLimits string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fw_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/verifyApiKey":
			w.Header().Set("x-fireworks-account-id", "acme")
		case "/v1/accounts/acme/users/felix/usageLimits":
			if userLimits == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(userLimits))
		case "/v1/accounts/acme/billing/summary":
			if r.URL.Query().Get("startTime") != "2026-10-01T00:00:00Z" || r.URL.Query().Get("endTime") != "2026-10-04T00:00:00Z" {
				t.Errorf("billing window %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"lineItems":[{"totalCost":{"currencyCode":"USD","units":"12","nanos":50000000}},{"totalCost":{"currencyCode":"USD","units":"3"}}]}`))
		case "/v1/accounts/acme/quotas":
			_, _ = w.Write([]byte(`{"quotas":[{"name":"accounts/acme/quotas/h100-us-iowa-1","value":"8"},{"name":"accounts/acme/quotas/monthly-spend-usd","value":"200"}]}`))
		case "/v1/accounts/acme":
			_, _ = w.Write([]byte(`{"name":"accounts/acme","suspendState":"UNSUSPENDED"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func client(srv *httptest.Server) *Client {
	return &Client{BaseURL: srv.URL, Key: func(context.Context) (string, error) { return "fw_test", nil }}
}

var now = time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)

func TestReadPrefersTheMembersOwnCap(t *testing.T) {
	srv := fakeAPI(t, `{"used":{"currencyCode":"USD","units":"41","nanos":500000000},"effectiveLimit":{"currencyCode":"USD","units":"100"},"limitSource":"ACCOUNT_DEFAULT"}`)
	defer srv.Close()
	r, err := client(srv).Read(context.Background(), "", "felix", now)
	if err != nil {
		t.Fatal(err)
	}
	want := Reading{Scope: ScopeUser, AccountID: "acme", UsedUSD: 41.5, LimitUSD: 100}
	if r != want {
		t.Errorf("got %+v, want %+v", r, want)
	}
}

func TestReadSeesABlockedMember(t *testing.T) {
	srv := fakeAPI(t, `{"used":"100","effective_limit":"100","exceeded_until":"2026-11-01T00:00:00Z"}`)
	defer srv.Close()
	r, err := client(srv).Read(context.Background(), "acme", "felix", now)
	if err != nil || !r.LimitReached || r.UsedUSD != 100 {
		t.Errorf("got %+v, %v", r, err)
	}
}

// Without per-user limits (a personal account, or limits not enabled),
// the account's month spend and monthly-spend-usd quota are the reading.
func TestReadFallsBackToTheAccount(t *testing.T) {
	srv := fakeAPI(t, "")
	defer srv.Close()
	r, err := client(srv).Read(context.Background(), "", "felix", now)
	if err != nil {
		t.Fatal(err)
	}
	want := Reading{Scope: ScopeAccount, AccountID: "acme", UsedUSD: 15.05, LimitUSD: 200}
	if r.Scope != want.Scope || r.AccountID != want.AccountID || r.LimitUSD != want.LimitUSD || r.UsedUSD < 15.049 || r.UsedUSD > 15.051 {
		t.Errorf("got %+v, want %+v", r, want)
	}
}

func TestReadReportsARefusedKey(t *testing.T) {
	srv := fakeAPI(t, "")
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: func(context.Context) (string, error) { return "fw_wrong", nil }}
	if _, err := c.Read(context.Background(), "", "", now); !errors.Is(err, ErrAuth) {
		t.Errorf("err = %v", err)
	}
}

func TestKeySource(t *testing.T) {
	ctx := context.Background()
	env := func(v string) func(string) string { return func(string) string { return v } }
	ran := ""
	run := func(_ context.Context, cmd string) (string, error) { ran = cmd; return "fw_fromhelper\n", nil }

	if k, err := (KeySource{Getenv: env("fw_env")}).Key(ctx); err != nil || k != "fw_env" {
		t.Errorf("env: %q %v", k, err)
	}
	helper := "/Users/x/.fireconnect/bin/fireconnect key export"
	k, err := KeySource{Getenv: env(""), Helper: func() string { return helper }, Run: run}.Key(ctx)
	if err != nil || k != "fw_fromhelper" || ran != helper {
		t.Errorf("helper: %q %v ran %q", k, err, ran)
	}
	// Any other helper is never run.
	ran = ""
	for _, other := range []string{"", "~/bin/anthropic-key.sh", "op read op://vault/anthropic", "fireconnect status"} {
		_, err := KeySource{Getenv: env(""), Helper: func() string { return other }, Run: run}.Key(ctx)
		if !errors.Is(err, ErrNoKey) || ran != "" {
			t.Errorf("%q: err %v, ran %q", other, err, ran)
		}
	}
}

func TestIsFireConnectHelper(t *testing.T) {
	for cmd, want := range map[string]bool{
		"fireconnect key export":                                true,
		`node "/u/.fireconnect/cli/fireconnect.mjs" key export`: true,
		"/opt/homebrew/bin/fireconnect key   export":            true,
		"fireconnect key list":                                  false,
		"myfireconnect key export":                              false,
	} {
		if got := IsFireConnectHelper(cmd); got != want {
			t.Errorf("%q = %v, want %v", cmd, got, want)
		}
	}
}

// config.json can hold keys; only ssoAccountId is taken from it.
func TestIdentity(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".fireconnect")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"apiKey":"fw_secret","ssoAccountId":"sso-org"}`)
	if a, u := Identity(home); a != "sso-org" || u != "" {
		t.Errorf("config only: %q %q", a, u)
	}
	write("minted-key.json", `{"keyId":"k1","userName":"accounts/acme/users/felix","displayName":"fireconnect-mbp"}`)
	if a, u := Identity(home); a != "acme" || u != "felix" {
		t.Errorf("minted: %q %q", a, u)
	}
	if a, u := Identity(t.TempDir()); a != "" || u != "" {
		t.Errorf("empty home: %q %q", a, u)
	}
}

func TestEnvelopeCarriesNoKey(t *testing.T) {
	env := NewEnvelope(now, Reading{Scope: ScopeUser, AccountID: "acme", UsedUSD: 41.5, LimitUSD: 100})
	for k, v := range env.Attributes {
		if strings.Contains(v, "fw_") {
			t.Errorf("%s carries a key: %q", k, v)
		}
	}
	if env.Attributes["extra_usage_used"] != "41.50" || env.Attributes["extra_usage_limit"] != "100.00" ||
		env.Attributes["billing"] != "per_token" || env.Attributes["granularity"] != "quota_snapshot" {
		t.Errorf("attrs %v", env.Attributes)
	}
}
