package accounts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSailResearchBalanceAndSpendOverTheRangeApplied(t *testing.T) {
	srv := serve(t, "/v2/usage/summary", "sail", fixture(t, "sailresearch"))
	defer srv.Close()
	got, err := SailResearch{BaseURL: srv.URL}.Read(context.Background(), "sail")
	if err != nil || !approx(got.BalanceUSD, 123.4567) || !got.HasBalance || !approx(got.UsedUSD, 12.34) || !got.HasUsed ||
		got.UsedPeriod != 30*24*time.Hour || got.Scope != "organization" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSailResearchShapes(t *testing.T) {
	const ok = `"object":"usage.summary","available":true,"has_metronome_customer":true`
	for _, tc := range []struct {
		body       string
		ok         bool
		hasBalance bool
		period     time.Duration
	}{
		// A free organisation given seven days for thirty.
		{`{` + ok + `,"balance_unavailable":false,"balance":-50,"period_spend":10,"effective_range":"7d"}`, true, true, 7 * 24 * time.Hour},
		// A failed balance keeps the spend.
		{`{` + ok + `,"balance_unavailable":true,"period_spend":10,"effective_range":"period"}`, true, false, 0},
		{`{` + ok + `,"balance_unavailable":false,"period_spend":10,"effective_range":"90d","balance":1}`, false, false, 0},
		{`{` + ok + `,"balance_unavailable":false,"period_spend":-1,"effective_range":"30d","balance":1}`, false, false, 0},
		{`{"object":"usage.summary","available":false}`, false, false, 0},
		{`{"object":"usage.summary","available":true,"has_metronome_customer":false,"balance_unavailable":false}`, false, false, 0},
		{`{"object":"list"}`, false, false, 0},
	} {
		srv := serve(t, "/v2/usage/summary", "k", tc.body)
		got, err := SailResearch{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (got.HasBalance != tc.hasBalance || got.UsedPeriod != tc.period)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}

// A 402 is the key disabled because the credits are exhausted.
func TestSailResearchExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusPaymentRequired) }))
	defer srv.Close()
	got, err := SailResearch{BaseURL: srv.URL}.Read(context.Background(), "k")
	if err != nil || !got.LimitReached {
		t.Fatalf("%+v %v", got, err)
	}
}
