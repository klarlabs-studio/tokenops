package accounts

import (
	"context"
	"testing"
)

func TestTripoReadsTheAvailableCredits(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		credits float64
	}{
		// Frozen credits are already out of the balance.
		{fixture(t, "tripo"), true, 2500},
		{`{"code":0,"data":{"balance":0,"frozen":0}}`, true, 0},
		{`{"code":1001,"message":"x"}`, false, 0},
		{`{"code":false,"data":{"balance":100,"frozen":0}}`, false, 0},
		{`{"code":0,"data":{"balance":-1,"frozen":0}}`, false, 0},
		{`{"code":0,"data":{"balance":1}}`, false, 0},
	} {
		srv := serve(t, "/v3/account/balance", "k", tc.body)
		got, err := Tripo{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (!got.HasCredits || got.Credits != tc.credits || got.CreditsUnit != "credits")) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
