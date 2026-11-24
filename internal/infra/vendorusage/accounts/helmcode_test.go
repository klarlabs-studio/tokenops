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

// helmcodeServer serves the dashboard API to the cookie "hc=ok"; an empty
// billing or credits answer is a 404.
func helmcodeServer(t *testing.T, quota, billing, credits string, plan ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "hc=ok" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		routes := map[string]string{"/api/usage/quota": quota, "/api/billing": billing, "/api/billing/credits": credits}
		if len(plan) > 0 {
			routes["/api/usage/credits"] = plan[0]
		}
		body := routes[r.URL.Path]
		if body == "" || r.Header.Get("Origin") != "https://cloud.helmcode.com" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHelmcodeReadsTheQuotas(t *testing.T) {
	srv := helmcodeServer(t, fixture(t, "helmcode"), `{"subscription":{"premium":true}}`, `{"balanceMicros":12500000,"currency":"eur"}`)
	got, err := Helmcode{BaseURL: srv.URL}.Read(context.Background(), "hc=ok")
	if err != nil || !got.Subscription || len(got.Windows) != 3 {
		t.Fatalf("%+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "kimi-k2 month" || !approx(w.UsedPct, 75) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("most used %+v", w)
	}
	if w := got.Windows[1]; w.Name != "glm-5 month" || !approx(w.UsedPct, 25) {
		t.Errorf("second %+v", w)
	}
	if w := got.Windows[2]; w.Name != "glm-5 5h" || w.Duration != 5*time.Hour || !approx(w.UsedPct, 20) {
		t.Errorf("rolling %+v", w)
	}
	if !got.HasCredits || !approx(got.Credits, 12.5) || got.CreditsUnit != "EUR" || got.HasBalance {
		t.Errorf("balance %+v", got)
	}
}

// Without a premium subscription the rolling tiers are left out; billing
// failures do not hide the quota.
func TestHelmcodeWithoutBilling(t *testing.T) {
	srv := helmcodeServer(t, fixture(t, "helmcode"), "", "")
	got, err := Helmcode{BaseURL: srv.URL}.Read(context.Background(), "Cookie: hc=ok")
	if err != nil || len(got.Windows) != 2 || got.HasCredits || got.HasBalance {
		t.Errorf("%+v, %v", got, err)
	}
}

func TestHelmcodeRefusalsAndShape(t *testing.T) {
	srv := helmcodeServer(t, fixture(t, "helmcode"), "", "")
	for _, cred := range []string{"hc=expired", "token"} {
		if _, err := (Helmcode{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
	bad := helmcodeServer(t, `{"models":"nope"}`, "", "")
	if got, err := (Helmcode{BaseURL: bad.URL}).Read(context.Background(), "hc=ok"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown shape %+v, %v", got, err)
	}
}

// A tenant on credits reads its credit plan, not the frozen token quotas;
// a disabled plan keeps them (CodexBar 677f9437f8,
// Tests/CodexBarTests/Fixtures/Providers/Helmcode/credits-usage.json).
func TestHelmcodeCreditPlan(t *testing.T) {
	srv := helmcodeServer(t, fixture(t, "helmcode"), `{"subscription":{"premium":true}}`, `{"balanceMicros":12500000,"currency":"usd"}`,
		`{"enabled":true,"tier":"inference","periodStart":"2026-11-06T16:01:55Z","periodEnd":"2026-12-06T16:01:55Z","allowanceCredits":7000,"usedCredits":10.6,"remainingCredits":6989.4}`)
	got, err := Helmcode{BaseURL: srv.URL}.Read(context.Background(), "hc=ok")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "credits" || !approx(got.Windows[0].UsedPct, 0.151428) ||
		!got.Windows[0].ResetsAt.Equal(time.Date(2026, 12, 6, 16, 1, 55, 0, time.UTC)) || got.Windows[0].Duration != 30*24*time.Hour ||
		!got.HasBalance || !approx(got.BalanceUSD, 12.5) {
		t.Fatalf("%+v, %v", got, err)
	}
	srv = helmcodeServer(t, fixture(t, "helmcode"), `{"subscription":{"premium":true}}`, "", `{"enabled":false}`)
	if got, err := (Helmcode{BaseURL: srv.URL}).Read(context.Background(), "hc=ok"); err != nil || len(got.Windows) != 3 {
		t.Errorf("disabled plan: %+v, %v", got, err)
	}
	for _, body := range []string{`{"tier":"x"}`, `{"enabled":true,"allowanceCredits":0,"usedCredits":1}`} {
		srv = helmcodeServer(t, fixture(t, "helmcode"), "", "", body)
		if _, err := (Helmcode{BaseURL: srv.URL}).Read(context.Background(), "hc=ok"); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s: %v", body, err)
		}
	}
}
