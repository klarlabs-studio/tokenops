package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func tinyAPIServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "__Secure-authjs.session-token=fixture" || r.URL.Path != "/api/user/credits" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"UNAUTHORIZED"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestTinyAPIReadsTheAvailableCredits(t *testing.T) {
	srv := tinyAPIServer(t, fixture(t, "tinyapi"))
	defer srv.Close()
	got, err := TinyAPI{BaseURL: srv.URL}.Read(context.Background(), "__Secure-authjs.session-token=fixture")
	if err != nil || !got.HasCredits || !approx(got.Credits, 1234.5) || got.CreditsUnit != "credits" || got.HasBalance {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := (TinyAPI{BaseURL: srv.URL}).Read(context.Background(), "__Secure-authjs.session-token=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired: %v", err)
	}
}

func TestTinyAPIShapes(t *testing.T) {
	for _, body := range []string{`{"success":false,"data":{"totalAvailable":50}}`, `{"success":true,"data":{"totalAvailable":-1}}`, `{"success":true}`} {
		srv := tinyAPIServer(t, body)
		_, err := TinyAPI{BaseURL: srv.URL}.Read(context.Background(), "__Secure-authjs.session-token=fixture")
		srv.Close()
		if err == nil {
			t.Errorf("%s: read", body)
		}
	}
}
