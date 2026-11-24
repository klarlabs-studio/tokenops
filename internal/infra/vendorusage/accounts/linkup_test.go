package accounts

import (
	"context"
	"testing"
)

func TestLinkupReadsTheUSDBalance(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		balance float64
	}{
		{fixture(t, "linkup"), true, 17.25},
		{`{"balance":-2}`, true, -2},
		{`{"balance":0}`, true, 0},
		{`{"balance":"5"}`, false, 0},
		{`{}`, false, 0},
	} {
		srv := serve(t, "/v1/credits/balance", "k", tc.body)
		got, err := Linkup{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (!got.HasBalance || !approx(got.BalanceUSD, tc.balance))) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
