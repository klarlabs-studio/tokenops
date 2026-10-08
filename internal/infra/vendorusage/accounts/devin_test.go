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

// devinServer answers the quota for the token "auth1_tok" on path, and
// records the paths and organisation header it was asked with.
func devinServer(t *testing.T, path, body string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path+"|"+r.Header.Get("x-cog-org-id"))
		if r.Header.Get("Authorization") != "Bearer auth1_tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDevinReadsTheQuota(t *testing.T) {
	var seen []string
	srv := devinServer(t, "/api/acme/billing/quota/usage", fixture(t, "devin"), &seen)
	got, err := Devin{BaseURL: srv.URL}.Read(context.Background(), "https://app.devin.ai/org/acme/settings/usage:Bearer auth1_tok")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// /api/org/acme is tried first, then /api/acme, as Devin's app does.
	if len(seen) != 2 || seen[0] != "/api/org/acme/billing/quota/usage|" {
		t.Errorf("paths %v", seen)
	}
	pst := time.FixedZone("", -8*3600)
	day, week := got.Windows[0], got.Windows[1]
	// 0.12 is a fraction: 12%.
	if day.Name != "day" || !approx(day.UsedPct, 12) || day.Duration != 24*time.Hour || !day.ResetsAt.Equal(time.Date(2026, 6, 11, 0, 0, 0, 0, pst)) {
		t.Errorf("day %+v", day)
	}
	if week.Name != "week" || !approx(week.UsedPct, 42) || !week.ResetsAt.Equal(time.Date(2026, 6, 14, 0, 0, 0, 0, pst)) {
		t.Errorf("week %+v", week)
	}
	if !got.HasBalance || !approx(got.BalanceUSD, 70.87) {
		t.Errorf("extra usage %+v", got)
	}
}

func TestDevinInternalOrgAndLegacyShape(t *testing.T) {
	var seen []string
	legacy := `{"plan_name":"pro","quota_usage":{"daily_quota":{"used":3,"limit":10,"reset_at":"2026-06-01T08:00:00Z"},` +
		`"weekly_quota":{"remaining_percent":0.25,"next_reset_at":1780560000}},"hide_daily_quota":"true","overage_balance_cents":7087}`
	srv := devinServer(t, "/api/org_abc12345/billing/quota/usage", legacy, &seen)
	got, err := Devin{BaseURL: srv.URL}.Read(context.Background(), "org_abc12345:auth1_tok")
	if err != nil || len(got.Windows) != 2 || seen[0] != "/api/org_abc12345/billing/quota/usage|org_abc12345" {
		t.Fatalf("got %+v, %v, %v", got, err, seen)
	}
	// Only a JSON true hides the daily window.
	if got.Windows[0].Name != "day" || !approx(got.Windows[0].UsedPct, 30) || !got.Windows[0].ResetsAt.Equal(time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("day %+v", got.Windows[0])
	}
	if got.Windows[1].Name != "week" || !approx(got.Windows[1].UsedPct, 75) || !got.Windows[1].ResetsAt.Equal(time.Unix(1780560000, 0)) {
		t.Errorf("week %+v", got.Windows[1])
	}
	if !approx(got.BalanceUSD, 70.87) {
		t.Errorf("cents balance %v", got.BalanceUSD)
	}
	hidden := devinServer(t, "/api/org/acme/billing/quota/usage", `{"daily_percentage":5,"weekly_percentage":7,"hide_daily_quota":true}`, &seen)
	got, err = Devin{BaseURL: hidden.URL}.Read(context.Background(), "acme:auth1_tok")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "week" {
		t.Errorf("hidden daily = %+v, %v", got, err)
	}
}

func TestDevinRefusals(t *testing.T) {
	var seen []string
	srv := devinServer(t, "/api/org/acme/billing/quota/usage", fixture(t, "devin"), &seen)
	// An expired token is refused, and no other path is tried with it.
	if _, err := (Devin{BaseURL: srv.URL}).Read(context.Background(), "acme:auth1_expired"); !errors.Is(err, usage.ErrAuth) || len(seen) != 1 {
		t.Errorf("expired token = %v after %v", err, seen)
	}
	for _, bad := range []string{"auth1_tok", ":auth1_tok", "acme:", "https://example.com/org/acme:auth1_tok", "../x:auth1_tok"} {
		if _, err := (Devin{BaseURL: srv.URL}).Read(context.Background(), bad); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("credential %q = %v, want ErrAuth", bad, err)
		}
	}
	noOrg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"No organizations found for auth1 user"}`))
	}))
	defer noOrg.Close()
	if _, err := (Devin{BaseURL: noOrg.URL}).Read(context.Background(), "acme:auth1_tok"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no organisation = %v, want ErrAuth", err)
	}
}

func TestDevinUnknownShape(t *testing.T) {
	var seen []string
	for _, body := range []string{`{"plan":"free"}`, `[]`, `{"daily_percentage":true}`} {
		srv := devinServer(t, "/api/org/acme/billing/quota/usage", body, &seen)
		if _, err := (Devin{BaseURL: srv.URL}).Read(context.Background(), "acme:auth1_tok"); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s = %v, want a parse error", body, err)
		}
	}
}

// The session setup reads from app.devin.ai's localStorage: the auth1
// token and the organisation last used.
func TestDevinCredentialFromLocalStorage(t *testing.T) {
	bundle := `{"@@devin@@::auth1_session":"{\"token\":\"auth1_0123456789abcdefghij\"}","last-internal-org-for-external-org-v1-acme":"\"org_abc123\"","last-internal-org-for-external-org-v1-null":"x"}`
	org, token, ok := devinCredential(bundle)
	if !ok || token != "auth1_0123456789abcdefghij" || org.internal != "org_abc123" || org.slug != "acme" {
		t.Errorf("%+v %q %v", org, token, ok)
	}
	for _, bad := range []string{`{}`, `{"x_auth1_session":"{\"token\":\"auth1_t\"}"}`, `{"x_auth1_session":"{\"token\":\"not-auth1\"}","last-internal-org-for-external-org-v1-a":"org_1"}`, `{not json`} {
		if _, _, ok := devinCredential(bad); ok {
			t.Errorf("%s accepted", bad)
		}
	}
}
