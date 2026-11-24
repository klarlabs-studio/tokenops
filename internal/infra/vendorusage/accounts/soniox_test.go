package accounts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sonioxServer serves the balance and the month from one fixture object.
func sonioxServer(t *testing.T, balance, monthly string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer org-key" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/v1/organization/balance":
			_, _ = w.Write([]byte(balance))
		case "/v1/organization/usage/monthly":
			_, _ = w.Write([]byte(monthly))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestSonioxBalanceAndMonthlySpendAgainstTheBudget(t *testing.T) {
	var f map[string]any
	if err := json.Unmarshal([]byte(fixture(t, "soniox")), &f); err != nil {
		t.Fatal(err)
	}
	bal, _ := json.Marshal(map[string]any{"available_balance_usd": f["available_balance_usd"], "invoice_billing_enabled": f["invoice_billing_enabled"]})
	mon, _ := json.Marshal(map[string]any{"month": f["month"], "usage_usd": f["usage_usd"], "monthly_budget_usd": f["monthly_budget_usd"]})
	srv := sonioxServer(t, string(bal), string(mon))
	defer srv.Close()
	got, err := Soniox{BaseURL: srv.URL}.Read(context.Background(), "org-key")
	reset := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err != nil || !approx(got.BalanceUSD, 18.5) || !got.HasBalance || !approx(got.UsedUSD, 6) || !approx(got.LimitUSD, 100) ||
		len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 6) || !got.Windows[0].ResetsAt.Equal(reset) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSonioxShapes(t *testing.T) {
	const month = `{"month":"2026-10","usage_usd":"6.00","monthly_budget_usd":"100.00"}`
	for _, tc := range []struct {
		name, balance, monthly string
		ok, hasBalance         bool
		windows                int
	}{
		// Billed by invoice: no prepaid balance to claim.
		{"invoice", `{"available_balance_usd":"0","invoice_billing_enabled":true}`, month, true, false, 1},
		{"negative balance", `{"available_balance_usd":"-3.5","invoice_billing_enabled":false}`, month, true, true, 1},
		{"no budget", `{"available_balance_usd":"1","invoice_billing_enabled":false}`, `{"month":"2026-10","usage_usd":"0","monthly_budget_usd":"0"}`, true, true, 0},
		{"number", `{"available_balance_usd":18.5,"invoice_billing_enabled":false}`, month, false, false, 0},
		{"no flag", `{"available_balance_usd":"1"}`, month, false, false, 0},
		{"bad month", `{"available_balance_usd":"1","invoice_billing_enabled":false}`, `{"month":"2026-13","usage_usd":"0","monthly_budget_usd":"0"}`, false, false, 0},
		{"negative use", `{"available_balance_usd":"1","invoice_billing_enabled":false}`, `{"month":"2026-10","usage_usd":"-1","monthly_budget_usd":"0"}`, false, false, 0},
	} {
		srv := sonioxServer(t, tc.balance, tc.monthly)
		got, err := Soniox{BaseURL: srv.URL}.Read(context.Background(), "org-key")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (got.HasBalance != tc.hasBalance || len(got.Windows) != tc.windows)) {
			t.Errorf("%s: %+v %v", tc.name, got, err)
		}
	}
}
