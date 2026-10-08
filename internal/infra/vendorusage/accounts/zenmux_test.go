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

// zenMuxServer answers the two Management API routes for the key "zm".
func zenMuxServer(t *testing.T, detail, balance string, balanceStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer zm" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/management/subscription/detail":
			_, _ = w.Write([]byte(detail))
		case "/api/v1/management/payg/balance":
			w.WriteHeader(balanceStatus)
			_, _ = w.Write([]byte(balance))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestZenMuxReadsQuotasAndBalance(t *testing.T) {
	srv := zenMuxServer(t, fixture(t, "zenmux"), `{"success":true,"data":{"currency":"usd","total_credits":12.34}}`, http.StatusOK)
	got, err := ZenMux{BaseURL: srv.URL}.Read(context.Background(), "zm")
	if err != nil || !got.Subscription || len(got.Windows) != 2 || !got.HasBalance || !approx(got.BalanceUSD, 12.34) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 35) || !w.ResetsAt.Equal(time.Date(2026, 10, 8, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 12.5) {
		t.Errorf("week %+v", w)
	}
}

func TestZenMuxBalanceIsBestEffort(t *testing.T) {
	failing := zenMuxServer(t, fixture(t, "zenmux"), `oops`, http.StatusInternalServerError)
	got, err := ZenMux{BaseURL: failing.URL}.Read(context.Background(), "zm")
	if err != nil || got.HasBalance || len(got.Windows) != 2 {
		t.Errorf("a failing balance = %+v, %v", got, err)
	}
	other := zenMuxServer(t, fixture(t, "zenmux"), `{"success":true,"data":{"currency":"cny","total_credits":5}}`, http.StatusOK)
	if got, err := (ZenMux{BaseURL: other.URL}).Read(context.Background(), "zm"); err != nil || got.HasBalance {
		t.Errorf("a balance in another currency = %+v, %v", got, err)
	}
	refused := zenMuxServer(t, fixture(t, "zenmux"), ``, http.StatusForbidden)
	if _, err := (ZenMux{BaseURL: refused.URL}).Read(context.Background(), "zm"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused balance = %v, want ErrAuth", err)
	}
}

func TestZenMuxRefusedAndUnknown(t *testing.T) {
	srv := zenMuxServer(t, fixture(t, "zenmux"), `{}`, http.StatusOK)
	if _, err := (ZenMux{BaseURL: srv.URL}).Read(context.Background(), "inference-key"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
	unknown := zenMuxServer(t, `{"success":false}`, `{}`, http.StatusOK)
	if _, err := (ZenMux{BaseURL: unknown.URL}).Read(context.Background(), "zm"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unsuccessful answer = %v", err)
	}
	empty := zenMuxServer(t, `{"success":true,"data":{"plan":{"tier":"free"}}}`, `{}`, http.StatusNotFound)
	if got, err := (ZenMux{BaseURL: empty.URL}).Read(context.Background(), "zm"); err != nil || !got.Empty() {
		t.Errorf("no quotas = %+v, %v", got, err)
	}
}
