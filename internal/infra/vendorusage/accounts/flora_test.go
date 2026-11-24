package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var floraNow = func() time.Time { return time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC) }

func floraServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ak_fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v1/workspaces/ws_fixture123/usage" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if q := r.URL.Query(); q.Get("start_date") != "2026-03-01" || q.Get("end_date") != "2026-03-17" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestFloraWorkspaceBalanceAndSpendThisMonth(t *testing.T) {
	srv := floraServer(t, fixture(t, "flora"))
	defer srv.Close()
	got, err := Flora{BaseURL: srv.URL, Now: floraNow}.Read(context.Background(), "ws_fixture123:ak_fixture")
	if err != nil || !approx(got.BalanceUSD, 128.4) || !got.HasBalance || !approx(got.UsedUSD, 12.5) || !got.HasUsed || got.Scope != "workspace" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := (Flora{BaseURL: srv.URL, Now: floraNow}).Read(context.Background(), "ws_other:ak_fixture"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown workspace: %v", err)
	}
	for _, cred := range []string{"ak_fixture", "workspace:ak_fixture", "ws_x:"} {
		if _, err := (Flora{}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", cred, err)
		}
	}
}

func TestFloraShapes(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok, has bool
		scope   string
	}{
		// Spend unavailable is said so, not $0.
		{`{"balance_scope":"caller","available_cost":7.25,"spend":{"status":"unavailable","reason":"permission_missing"}}`, true, false, "member"},
		{`{"available_cost":1,"spend":{"status":"available"}}`, false, false, ""},
		{`{"spend":{"status":"available","total_cost":1}}`, false, false, ""},
	} {
		srv := floraServer(t, tc.body)
		got, err := Flora{BaseURL: srv.URL, Now: floraNow}.Read(context.Background(), "ws_fixture123:ak_fixture")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (got.HasUsed != tc.has || got.Scope != tc.scope)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
