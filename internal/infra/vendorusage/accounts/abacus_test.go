package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestAbacusReadsCreditsAndTheBillingMonth(t *testing.T) {
	srv := newWebServer(t, "sessionid=s", http.StatusUnauthorized, routesOf(t, "abacus"))
	got, err := Abacus{BaseURL: srv.URL}.Read(context.Background(), "sessionid=s; csrftoken=c")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	reset := time.Date(2024, 3, 31, 12, 30, 0, 0, time.UTC)
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(reset) || w.Duration != reset.Sub(reset.AddDate(0, -1, 0)) {
		t.Errorf("month %+v", w)
	}
}

// Without the billing answer the credits stand, with no reset invented.
func TestAbacusWithoutBilling(t *testing.T) {
	routes := routesOf(t, "abacus")
	delete(routes, "/api/_getBillingInfo")
	srv := newWebServer(t, "sessionid=s", http.StatusUnauthorized, routes)
	got, err := Abacus{BaseURL: srv.URL}.Read(context.Background(), "sessionid=s")
	if err != nil || len(got.Windows) != 1 || !got.Windows[0].ResetsAt.IsZero() || got.Windows[0].Duration != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestAbacusRefusals(t *testing.T) {
	srv := newWebServer(t, "sessionid=s", http.StatusUnauthorized, routesOf(t, "abacus"))
	if _, err := (Abacus{BaseURL: srv.URL}).Read(context.Background(), "sessionid=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("401 = %v", err)
	}
	expired := newWebServer(t, "", 0, map[string]string{"/api/_getOrganizationComputePoints": `{"success":false,"error":"Session expired"}`})
	if _, err := (Abacus{BaseURL: expired.URL}).Read(context.Background(), "sessionid=s"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("an expired session in the body = %v", err)
	}
	odd := newWebServer(t, "", 0, map[string]string{"/api/_getOrganizationComputePoints": `{"success":true,"result":{}}`})
	if _, err := (Abacus{BaseURL: odd.URL}).Read(context.Background(), "sessionid=s"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an answer without credits = %v", err)
	}
}
