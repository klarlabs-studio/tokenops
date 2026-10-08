package accounts

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var nousNow = func() time.Time { return time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC) }

func nousToken(exp time.Time) string {
	claims := `{"sub":"u","exp":` + strconv.FormatInt(exp.Unix(), 10) + `}`
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".sig"
}

func TestNousReadsTheMonthlyGrantAndTopUp(t *testing.T) {
	tok := nousToken(nousNow().Add(time.Hour))
	srv := serve(t, "/api/oauth/account", tok, fixture(t, "nous"))
	defer srv.Close()
	got, err := Nous{BaseURL: srv.URL, Now: nousNow}.Read(context.Background(), tok)
	if err != nil || !got.Subscription || len(got.Windows) != 1 || !got.HasBalance || !approx(got.BalanceUSD, 12.5) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month %+v", w)
	}
}

func TestNousFreeTierShowsOnlyTheBalance(t *testing.T) {
	srv := serve(t, "/api/oauth/account", "opaque", `{"subscription":{"plan":"Free","monthly_credits":0},"paid_service_access":{"total_usable_credits":"3.00"}}`)
	defer srv.Close()
	got, err := Nous{BaseURL: srv.URL}.Read(context.Background(), "opaque")
	if err != nil || got.Subscription || len(got.Windows) != 0 || !got.HasBalance || got.BalanceUSD != 3 {
		t.Errorf("free tier = %+v, %v", got, err)
	}
	unknown := serve(t, "/api/oauth/account", "opaque", `{"error":"server_error"}`)
	defer unknown.Close()
	if _, err := (Nous{BaseURL: unknown.URL}).Read(context.Background(), "opaque"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("a reported error = %v", err)
	}
}

func TestNousRefusesExpiredAndRejectedTokens(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	expired := nousToken(nousNow().Add(-time.Minute))
	if _, err := (Nous{BaseURL: srv.URL, Now: nousNow}).Read(context.Background(), expired); !errors.Is(err, usage.ErrAuth) || called {
		t.Errorf("an expired token = %v (sent: %v)", err, called)
	}
	if _, err := (Nous{BaseURL: srv.URL, Now: nousNow}).Read(context.Background(), nousToken(nousNow().Add(time.Hour))); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a rejected token = %v, want ErrAuth", err)
	}
}
