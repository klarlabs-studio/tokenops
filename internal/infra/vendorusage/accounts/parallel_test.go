package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func parallelServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/rpc/billing/payments/refresh-balance" ||
			r.Header.Get("Cookie") != "session=fixture" || r.Header.Get("Origin") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestParallelReadsTheBalanceInCents(t *testing.T) {
	srv := parallelServer(t, http.StatusOK, "application/json", fixture(t, "parallel"))
	defer srv.Close()
	got, err := Parallel{BaseURL: srv.URL}.Read(context.Background(), "session=fixture")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 123.45) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestParallelShapes(t *testing.T) {
	for _, tc := range []struct {
		status            int
		contentType, body string
		ok, auth          bool
	}{
		{http.StatusOK, "application/json", `{"kind":"ok","data":{"balance":0,"pendingDebitBalanceCents":0}}`, true, false},
		{http.StatusOK, "text/html; charset=utf-8", `<html>sign in</html>`, false, true},
		{http.StatusFound, "text/html", ``, false, true},
		{http.StatusOK, "application/json", `{"kind":"error","data":{"balance":5}}`, false, false},
		{http.StatusOK, "application/json", `{"kind":"ok","data":{"balance":"5"}}`, false, false},
		{http.StatusServiceUnavailable, "application/json", `{}`, false, false},
	} {
		srv := parallelServer(t, tc.status, tc.contentType, tc.body)
		_, err := Parallel{BaseURL: srv.URL}.Read(context.Background(), "session=fixture")
		srv.Close()
		if (err == nil) != tc.ok || errors.Is(err, usage.ErrAuth) != tc.auth {
			t.Errorf("%d %s: %v", tc.status, tc.body, err)
		}
	}
	if _, err := (Parallel{}).Read(context.Background(), "par-api-key"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("an API key: %v", err)
	}
}
