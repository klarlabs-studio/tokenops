package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const xapiSession = "auth_token=at; ct0=csrf"

func TestXAPIReadsPurchasedAndFreeCredit(t *testing.T) {
	srv := newWebServer(t, "auth_token=at", http.StatusUnauthorized, routesOf(t, "xapi"))
	got, err := XAPI{BaseURL: srv.URL}.Read(context.Background(), xapiSession)
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 12.4) || got.Subscription {
		t.Fatalf("got %+v, %v", got, err)
	}
	if srv.paths[0] != "/api/me" || srv.paths[1] != "/api/accounts/fixture-account/credits" {
		t.Errorf("paths %v", srv.paths)
	}
}

// An overdrawn account keeps its sign; an account ID given as a number is
// read; no free credit is zero.
func TestXAPINegativeBalanceAndNumericID(t *testing.T) {
	routes := map[string]string{
		"/api/me":                  `{"account":{"id":42}}`,
		"/api/accounts/42/credits": `{"credits":{"balance":-2.15}}`,
	}
	srv := newWebServer(t, "auth_token=at", http.StatusUnauthorized, routes)
	got, err := XAPI{BaseURL: srv.URL}.Read(context.Background(), xapiSession)
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, -2.15) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// The console's CSRF header echoes ct0, as its page sends it.
func TestXAPISendsTheCSRFToken(t *testing.T) {
	var csrf string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrf = r.Header.Get("X-Csrf-Token")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, _ = XAPI{BaseURL: srv.URL}.Read(context.Background(), xapiSession)
	if csrf != "csrf" {
		t.Errorf("X-Csrf-Token = %q", csrf)
	}
}

func TestXAPIRefusals(t *testing.T) {
	srv := newWebServer(t, "auth_token=at", http.StatusUnauthorized, routesOf(t, "xapi"))
	if _, err := (XAPI{BaseURL: srv.URL}).Read(context.Background(), "auth_token=expired; ct0=c"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	if _, err := (XAPI{BaseURL: srv.URL}).Read(context.Background(), "auth_token=at"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no ct0 = %v", err)
	}
	// Signed out, the console answers 400 with error code 215.
	out := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"code":215,"message":"Bad Authentication data."}]}`))
	}))
	defer out.Close()
	if _, err := (XAPI{BaseURL: out.URL}).Read(context.Background(), xapiSession); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("code 215 = %v, want ErrAuth", err)
	}
}

func TestXAPIUnknownShape(t *testing.T) {
	for _, routes := range []map[string]string{
		{"/api/me": `{"account":{"id":"../x"}}`},
		{"/api/me": `{}`},
		{"/api/me": `{"account":{"id":"a"}}`, "/api/accounts/a/credits": `{"credits":{"balance":"10"}}`},
		{"/api/me": `{"account":{"id":"a"}}`, "/api/accounts/a/credits": `{"credits":{"balance":1},"freeCredits":{"balance":null}}`},
		{"/api/me": `{"account":{"id":"a"}}`, "/api/accounts/a/credits": `<html></html>`},
	} {
		srv := newWebServer(t, "auth_token=at", http.StatusUnauthorized, routes)
		if got, err := (XAPI{BaseURL: srv.URL}).Read(context.Background(), xapiSession); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%v = %+v, %v; want a parse error", routes, got, err)
		}
	}
}
