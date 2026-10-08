package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestHyperReadsWithTheSession(t *testing.T) {
	srv := newWebServer(t, "session=hs", http.StatusUnauthorized, map[string]string{"/v1/credits": `{"balance":0}`})
	got, err := HyperWeb{BaseURL: srv.URL}.Read(context.Background(), "session=hs")
	if err != nil || !got.HasCredits || got.Credits != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := (HyperWeb{BaseURL: srv.URL}).Read(context.Background(), "session=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	// A signed-out session gets the site's page, not the balance.
	page := newWebServer(t, "", 0, map[string]string{"/v1/credits": `<!doctype html><html><a href="/login">Sign in</a></html>`})
	if _, err := (HyperWeb{BaseURL: page.URL}).Read(context.Background(), "session=hs"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("web page = %v", err)
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
