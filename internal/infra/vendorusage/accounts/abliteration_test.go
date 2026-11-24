package accounts

import (
	"context"
	"testing"
)

func TestAbliterationBalanceIsCreditsLessUsage(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		balance float64
	}{
		{fixture(t, "abliteration"), true, 49.989363},
		// Overspent is floored at zero.
		{`{"data":{"total_credits":50,"total_usage":60}}`, true, 0},
		{`{"data":{"total_credits":0,"total_usage":0}}`, true, 0},
		{`{"data":{"total_credits":50}}`, false, 0},
		{`{"data":{"total_credits":-1,"total_usage":0}}`, false, 0},
		{`{"data":{"total_credits":"50","total_usage":0}}`, false, 0},
		{`{}`, false, 0},
	} {
		srv := serve(t, "/v1/credits", "k", tc.body)
		got, err := Abliteration{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (!got.HasBalance || !approx(got.BalanceUSD, tc.balance) || got.HasUsed)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
