package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestFutureAGICreditsAndRequestsOverTheBillingPeriod(t *testing.T) {
	srv := serveHeaders(t, "/usage/v2/usage-overview/", map[string]string{"X-Api-Key": "fi-key", "X-Secret-Key": "fi-secret"}, fixture(t, "futureagi"))
	defer srv.Close()
	got, err := FutureAGI{BaseURL: srv.URL}.Read(context.Background(), "fi-key:fi-secret")
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err != nil || len(got.Windows) != 2 || got.Windows[0].Name != "month (AI credits)" || !approx(got.Windows[0].UsedPct, 25) ||
		!got.Windows[0].ResetsAt.Equal(end) || got.Windows[0].Duration != 31*24*time.Hour ||
		got.Windows[1].Name != "month (gateway requests)" || got.Windows[1].UsedPct != 100 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := (FutureAGI{}).Read(context.Background(), "fi-key"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no secret: %v", err)
	}
}

func TestFutureAGIShapes(t *testing.T) {
	period := `"billing_period_start":"2026-10-01","billing_period_end":"2026-11-01"`
	for _, tc := range []struct {
		body    string
		ok      bool
		windows int
	}{
		{`{"status":true,"result":{` + period + `,"dimensions":[{"key":"ai_credits","current_usage":1,"free_allowance":0}]}}`, true, 0},
		{`{"status":true,"result":{` + period + `,"dimensions":[]}}`, false, 0},
		{`{"status":true,"result":{"billing_period_start":"2026-11-01","billing_period_end":"2026-10-01","dimensions":[{"key":"a","current_usage":1,"free_allowance":1}]}}`, false, 0},
		{`{"status":true,"result":{` + period + `,"dimensions":[{"key":"a","current_usage":1,"free_allowance":1},{"key":"a","current_usage":1,"free_allowance":1}]}}`, false, 0},
		{`{"status":true,"result":{` + period + `,"dimensions":[{"key":"a","current_usage":-1,"free_allowance":1}]}}`, false, 0},
		{`{"status":false}`, false, 0},
	} {
		srv := serveHeaders(t, "/usage/v2/usage-overview/", map[string]string{"X-Api-Key": "k"}, tc.body)
		got, err := FutureAGI{BaseURL: srv.URL}.Read(context.Background(), "k:s")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && len(got.Windows) != tc.windows) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
