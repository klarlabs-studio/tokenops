package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const mimoSession = "api-platform_serviceToken=svc; userId=42"

func TestMiMoReadsBalanceAndTokenPlan(t *testing.T) {
	srv := newWebServer(t, "api-platform_serviceToken=svc", http.StatusUnauthorized, routesOf(t, "mimo"))
	got, err := MiMo{BaseURL: srv.URL}.Read(context.Background(), mimoSession)
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 25.51) || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	w := got.Windows[0]
	if w.Name != "month" || !approx(w.UsedPct, 5.05) || !w.ResetsAt.Equal(time.Date(2026, 5, 4, 23, 59, 59, 0, time.UTC)) || w.Duration != 30*24*time.Hour {
		t.Errorf("month %+v", w)
	}
}

// Without a Token Plan the balance is still read; a balance in another
// currency is not reported as dollars.
func TestMiMoBalanceOnly(t *testing.T) {
	routes := map[string]string{"/api/v1/balance": `{"code":0,"data":{"balance":"80.00","currency":"CNY"}}`}
	srv := newWebServer(t, "api-platform_serviceToken=svc", http.StatusUnauthorized, routes)
	got, err := MiMo{BaseURL: srv.URL}.Read(context.Background(), mimoSession)
	if err != nil || got.HasBalance || got.Subscription || !got.Empty() {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := (MiMo{BaseURL: newWebServer(t, "", 0, map[string]string{"/api/v1/balance": `{"code":0,"data":{}}`}).URL}).Read(context.Background(), mimoSession); err == nil {
		t.Error("an answer without a balance was read")
	}
}

func TestMiMoRefusals(t *testing.T) {
	srv := newWebServer(t, "api-platform_serviceToken=svc", http.StatusFound, routesOf(t, "mimo"))
	if _, err := (MiMo{BaseURL: srv.URL}).Read(context.Background(), "api-platform_serviceToken=old; userId=42"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("redirected session = %v", err)
	}
	body := newWebServer(t, "", 0, map[string]string{"/api/v1/balance": `{"code":401,"message":"login required"}`})
	if _, err := (MiMo{BaseURL: body.URL}).Read(context.Background(), mimoSession); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refusal in the body = %v", err)
	}
}
