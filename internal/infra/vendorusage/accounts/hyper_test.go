package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestHyperReadsHypercreditsWithAKey(t *testing.T) {
	srv := serve(t, "/v1/credits", "hk", fixture(t, "hyper"))
	defer srv.Close()
	got, err := Hyper{BaseURL: srv.URL}.Read(context.Background(), "hk")
	// Hypercredits are kept in their own unit, never dollars.
	if err != nil || !got.HasCredits || got.Credits != 42.5 || got.CreditsUnit != "Hypercredits" || got.HasBalance {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := (Hyper{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused key = %v", err)
	}
}

// hyperDashboardHTML mirrors the signed-in team dashboard (CodexBar
// 28ded89ecb, TestsPlugin/HyperPluginTests.swift).
const hyperDashboardHTML = `<html><head><title>Dashboard — Charm™ Hyper</title></head><body>
<div class="rounded-xl bg-card p-5">
  <p class="text-sm">Hypercredits Available</p>
  <div class="mt-1 flex items-center gap-1">
    <img src="/static/hexy.webp" alt="" width="34" height="34" />
    <p class="text-5xl font-bold">1,100.5</p>
  </div>
</div>
<div class="rounded-xl bg-card p-5">
  <p class="text-sm">API Keys</p>
  <p class="mt-1 text-5xl font-bold">0</p>
</div>
</body></html>`

// hyperSite is hyper.charm.land for the session "session=hs": / redirects
// to the team's dashboard; landing is what / serves otherwise.
func hyperSite(t *testing.T, landing string, dashboard string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Cookie") != "session=hs" {
			_, _ = w.Write([]byte(`<html><title>Charm™ Hyper — Built for Coding Agents</title><p>Hypercredits per month.</p></html>`))
			return
		}
		switch r.URL.Path {
		case "/":
			if landing != "" {
				_, _ = w.Write([]byte(landing))
				return
			}
			http.Redirect(w, r, "/teams/11111111-2222-3333-4444-555555555555/dashboard", http.StatusFound)
		case "/teams/11111111-2222-3333-4444-555555555555/dashboard":
			_, _ = w.Write([]byte(dashboard))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// /v1/credits takes bearer keys only; a session reads the team dashboard
// it lands on (CodexBar 28ded89ecb).
func TestHyperReadsTheDashboardWithTheSession(t *testing.T) {
	srv := hyperSite(t, "", hyperDashboardHTML)
	got, err := HyperWeb{BaseURL: srv.URL}.Read(context.Background(), "session=hs")
	if err != nil || !got.HasCredits || got.Credits != 1100.5 || got.CreditsUnit != "Hypercredits" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// The dashboard served at / itself, naming the team in a link.
	inline := hyperSite(t, strings.Replace(hyperDashboardHTML, "</body>", `<a href="/teams/11111111-2222-3333-4444-555555555555/billing">Upgrade</a></body>`, 1), "")
	if got, err := (HyperWeb{BaseURL: inline.URL}).Read(context.Background(), "session=hs"); err != nil || got.Credits != 1100.5 {
		t.Errorf("inline = %+v, %v", got, err)
	}
	// A signed-out session gets the marketing page, with no team.
	if _, err := (HyperWeb{BaseURL: srv.URL}).Read(context.Background(), "session=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	// A team page without the balance card is a changed page, not zero.
	empty := hyperSite(t, "", `<html><a href="/teams/11111111-2222-3333-4444-555555555555/x">x</a></html>`)
	if _, err := (HyperWeb{BaseURL: empty.URL}).Read(context.Background(), "session=hs"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("no balance = %v", err)
	}
}

// Each reader declines the other's credential without calling Hyper.
func TestHyperSplitsKeysAndSessions(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	if _, err := (Hyper{BaseURL: srv.URL}).Read(context.Background(), "session=hs"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("key reader given a session = %v", err)
	}
	if _, err := (HyperWeb{BaseURL: srv.URL}).Read(context.Background(), "hk"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("session reader given a key = %v", err)
	}
	if called {
		t.Error("Hyper was called")
	}
}

func TestHyperUnknownShape(t *testing.T) {
	for _, body := range []string{`""`, `{}`, `{"balance":`, `{"balance":-1}`, `{"balance":"invalid"}`, `{"balance":null}`, `{"balance":true}`, `{"balance":1e400}`, `null`, `[]`} {
		srv := serve(t, "/v1/credits", "hk", body)
		if got, err := (Hyper{BaseURL: srv.URL}).Read(context.Background(), "hk"); err == nil {
			t.Errorf("%s = %+v", body, got)
		}
		srv.Close()
	}
}
