package accounts

import (
	"context"
	"testing"
)

func TestSofyaAvailableCredits(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		credits float64
	}{
		{fixture(t, "sofya"), true, 1034},
		{`{"credits":0,"plan_credits":0,"purchased_credits":0,"is_free_tier":false}`, true, 0},
		{`{"plan_credits":5}`, false, 0},
		{`{"credits":-1}`, false, 0},
		{`{"credits":"5"}`, false, 0},
		{`{"email":"x"}`, false, 0},
	} {
		srv := serve(t, "/v1/auth/me", "k", tc.body)
		got, err := Sofya{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (!got.HasCredits || got.Credits != tc.credits || got.CreditsUnit != "credits" || len(got.Windows) != 0)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
