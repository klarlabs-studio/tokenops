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

const lithosCookie = "__Host-console_session=sess; __Host-console_csrf=csrf"

func lithosNow() time.Time { return time.Date(2027, 1, 21, 15, 0, 0, 0, time.UTC) }

func TestLithosAIReadsBalanceAndMonthSpend(t *testing.T) {
	srv := newWebServer(t, "__Host-console_session=sess", http.StatusUnauthorized, routesOf(t, "lithosai"))
	got, err := LithosAI{BaseURL: srv.URL, Now: lithosNow}.Read(context.Background(), lithosCookie)
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 4.70709986) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !got.HasUsed || !approx(got.UsedUSD, 2.360066994) || got.LimitUSD != 0 {
		t.Errorf("month spend %+v", got)
	}
}

// The organisation and CSRF headers go with the billing calls, as the
// console sends them.
func TestLithosAIHeaders(t *testing.T) {
	routes := routesOf(t, "lithosai")
	var org, csrf string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/billing" {
			org, csrf = r.Header.Get("X-Organization-Id"), r.Header.Get("X-Console-Csrf")
		}
		_, _ = w.Write([]byte(routes[r.URL.Path]))
	}))
	defer srv.Close()
	if _, err := (LithosAI{BaseURL: srv.URL, Now: lithosNow}).Read(context.Background(), lithosCookie); err != nil {
		t.Fatal(err)
	}
	if org != "org-fixture" || csrf != "csrf" {
		t.Errorf("org %q csrf %q", org, csrf)
	}
}

// A spend answer for another range, or none, leaves the balance alone; a
// negative balance keeps its sign.
func TestLithosAISpendUnavailable(t *testing.T) {
	routes := routesOf(t, "lithosai")
	routes["/api/billing"] = `{"balanceNanos":-1500000000,"hasCard":false,"onHold":true}`
	routes["/api/billing/spend"] = `{"start":"2026-12-01","end":"2027-01-21","days":[]}`
	srv := newWebServer(t, "__Host-console_session=sess", http.StatusUnauthorized, routes)
	got, err := LithosAI{BaseURL: srv.URL, Now: lithosNow}.Read(context.Background(), lithosCookie)
	if err != nil || !approx(got.BalanceUSD, -1.5) || got.HasUsed {
		t.Fatalf("got %+v, %v", got, err)
	}
	delete(routes, "/api/billing/spend")
	srv = newWebServer(t, "__Host-console_session=sess", http.StatusUnauthorized, routes)
	if got, err = (LithosAI{BaseURL: srv.URL, Now: lithosNow}).Read(context.Background(), lithosCookie); err != nil || got.HasUsed {
		t.Errorf("no spend route = %+v, %v", got, err)
	}
}

func TestLithosAIRefusals(t *testing.T) {
	srv := newWebServer(t, "__Host-console_session=sess", http.StatusUnauthorized, routesOf(t, "lithosai"))
	if _, err := (LithosAI{BaseURL: srv.URL, Now: lithosNow}).Read(context.Background(), "__Host-console_session=old; __Host-console_csrf=c"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	if _, err := (LithosAI{BaseURL: srv.URL}).Read(context.Background(), "__Host-console_session=sess"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no CSRF cookie = %v", err)
	}
}

func TestLithosAIUnknownShape(t *testing.T) {
	for _, change := range []map[string]string{
		{"/api/me": `{"activeOrganization":null}`},
		{"/api/me": `{"activeOrganization":{"id":"../etc"}}`},
		{"/api/billing": `{"balanceNanos":"4707099860"}`},
		{"/api/billing": `{"balanceNanos":1.5}`},
	} {
		routes := routesOf(t, "lithosai")
		for k, v := range change {
			routes[k] = v
		}
		srv := newWebServer(t, "__Host-console_session=sess", http.StatusUnauthorized, routes)
		if _, err := (LithosAI{BaseURL: srv.URL, Now: lithosNow}).Read(context.Background(), lithosCookie); err == nil {
			t.Errorf("%v was read", change)
		}
	}
}
