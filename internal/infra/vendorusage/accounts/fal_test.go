package accounts

import (
	"context"
	"testing"
)

func TestFalReadsTheCreditBalanceWithAKeyScheme(t *testing.T) {
	srv := serveRaw(t, "/v1/account/billing", "Key fal-key", fixture(t, "fal"))
	defer srv.Close()
	got, err := Fal{BaseURL: srv.URL}.Read(context.Background(), "fal-key")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 42.5) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestFalShapes(t *testing.T) {
	for _, tc := range []struct {
		body             string
		ok, usd, credits bool
		amount           float64
	}{
		{`{"credits":{"current_balance":"12.75","currency":"USD"}}`, true, true, false, 12.75},
		// A missing or malformed currency is USD, as fal's own clients read it.
		{`{"credits":{"current_balance":0,"currency":"dollars"}}`, true, true, false, 0},
		{`{"credits":{"current_balance":9,"currency":"EUR"}}`, true, false, true, 9},
		{`{"username":"x"}`, false, false, false, 0},
		{`{"credits":{"current_balance":null}}`, false, false, false, 0},
		{`{"credits":{"current_balance":"1e3"}}`, false, false, false, 0},
	} {
		srv := serveRaw(t, "/v1/account/billing", "Key k", tc.body)
		got, err := Fal{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (got.HasBalance != tc.usd || got.HasCredits != tc.credits ||
			(tc.usd && !approx(got.BalanceUSD, tc.amount)) || (tc.credits && !approx(got.Credits, tc.amount)))) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
