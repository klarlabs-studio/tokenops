package accounts

import (
	"context"
	"errors"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestTrustedRouterSpendAgainstTheKeyLimit(t *testing.T) {
	srv := serve(t, "/v1/key", "sk-tr", fixture(t, "trustedrouter"))
	defer srv.Close()
	got, err := TrustedRouter{BaseURL: srv.URL}.Read(context.Background(), "sk-tr")
	// Spent is the limit less what remains (in-flight requests included),
	// not the lifetime usage.
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 3.4) || !approx(got.LimitUSD, 10) || got.LimitReached || got.Scope != "key" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := (TrustedRouter{BaseURL: srv.URL}).Read(context.Background(), "other"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

func TestTrustedRouterShapes(t *testing.T) {
	const usageFields = `"usage_microdollars":3250000,"usage_daily_microdollars":400000,"usage_weekly_microdollars":1100000,"usage_monthly_microdollars":3250000`
	for _, tc := range []struct {
		body        string
		ok          bool
		used, limit float64
		reached     bool
	}{
		// No limit: this month's spend.
		{`{"data":{` + usageFields + `,"limit_microdollars":null,"limit_remaining_microdollars":null}}`, true, 3.25, 0, false},
		{`{"data":{` + usageFields + `,"limit_microdollars":5000000,"limit_remaining_microdollars":0}}`, true, 5, 5, true},
		// A zero limit is kept, with no block claimed.
		{`{"data":{` + usageFields + `,"limit_microdollars":0,"limit_remaining_microdollars":0}}`, true, 0, 0, false},
		{`{"data":{` + usageFields + `,"limit_microdollars":5000000,"limit_remaining_microdollars":null}}`, false, 0, 0, false},
		{`{"data":{` + usageFields + `}}`, false, 0, 0, false},
		{`{"data":{"usage":3.25,"usage_daily":0.4,"usage_weekly":1.1,"usage_monthly":3.25}}`, false, 0, 0, false},
		{`{"data":{"usage_microdollars":1.5,"usage_daily_microdollars":0,"usage_weekly_microdollars":0,"usage_monthly_microdollars":0,"limit_microdollars":null,"limit_remaining_microdollars":null}}`, false, 0, 0, false},
		{`{"data":{"usage_microdollars":-1,"usage_daily_microdollars":0,"usage_weekly_microdollars":0,"usage_monthly_microdollars":0,"limit_microdollars":null,"limit_remaining_microdollars":null}}`, false, 0, 0, false},
		{`{"data":[]}`, false, 0, 0, false},
	} {
		srv := serve(t, "/v1/key", "k", tc.body)
		got, err := TrustedRouter{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (!approx(got.UsedUSD, tc.used) || !approx(got.LimitUSD, tc.limit) || got.LimitReached != tc.reached)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
