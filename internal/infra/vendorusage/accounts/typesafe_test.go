package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// typeSafeConsole answers GET /api/billing/overview for the cookie "ts=ok"
// sent with same-origin headers, with status and body (CodexBar
// b22cdc2fd7, d1f1f2b3d0, TestsPlugin/TypeSafePluginTests.swift).
func typeSafeConsole(t *testing.T, status int, location, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/billing/overview" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Cookie") != "ts=ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("Origin") == "" || r.Header.Get("Referer") == "" ||
			r.Header.Get("Sec-Fetch-Site") != "same-origin" || r.Header.Get("Sec-Fetch-Mode") != "cors" ||
			r.Header.Get("Sec-Fetch-Dest") != "empty" || r.Header.Get("User-Agent") != browserUA {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"Cross-origin request refused"}`))
			return
		}
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTypeSafeReadsTheBillingOverview(t *testing.T) {
	srv := typeSafeConsole(t, http.StatusOK, "", fixture(t, "typesafe"))
	got, err := TypeSafe{BaseURL: srv.URL}.Read(context.Background(), "Cookie: ts=ok")
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 0.01) || !got.HasBalance || !approx(got.BalanceUSD, 4.98) || got.Scope != "account" {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestTypeSafeRefusals(t *testing.T) {
	srv := typeSafeConsole(t, http.StatusOK, "", fixture(t, "typesafe"))
	for _, cred := range []string{"ts=expired", "token"} {
		if _, err := (TypeSafe{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
	for name, tc := range map[string]struct {
		status         int
		location, body string
	}{
		"sign-in page":   {http.StatusOK, "", `<!doctype html><html><a href="/login">Sign in</a></html>`},
		"login redirect": {http.StatusSeeOther, "/login?next=/billing", ""},
		"sso elsewhere":  {http.StatusFound, "https://sso.example.com/authorize", ""},
	} {
		srv := typeSafeConsole(t, tc.status, tc.location, tc.body)
		if _, err := (TypeSafe{BaseURL: srv.URL}).Read(context.Background(), "ts=ok"); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s = %v, want ErrAuth", name, err)
		}
	}
}

// A shape TypeSafe changed, a benign redirect or another HTML page is an
// error, never zero and never a signed-out session.
func TestTypeSafeUnknownShape(t *testing.T) {
	for name, tc := range map[string]struct {
		status         int
		location, body string
	}{
		"no billing":   {http.StatusOK, "", `{"x":1}`},
		"no balance":   {http.StatusOK, "", `{"billing":{"spent":1}}`},
		"string spent": {http.StatusOK, "", `{"billing":{"spent":"1","balance":2}}`},
		"route moved":  {http.StatusMovedPermanently, "/api/v2/billing/overview", ""},
		"other html":   {http.StatusOK, "", `<html><body>maintenance</body></html>`},
		"not found":    {http.StatusNotFound, "", ``},
	} {
		srv := typeSafeConsole(t, tc.status, tc.location, tc.body)
		if got, err := (TypeSafe{BaseURL: srv.URL}).Read(context.Background(), "ts=ok"); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s = %+v, %v", name, got, err)
		}
	}
}
