package accounts

import (
	"context"
	"testing"
	"time"
)

func TestMunitoMonthSpendAgainstItsLimitAndTheDayWindow(t *testing.T) {
	srv := serve(t, "/admin/v1/limits", "mk-admin", fixture(t, "munito"))
	defer srv.Close()
	got, err := Munito{BaseURL: srv.URL}.Read(context.Background(), "mk-admin")
	if err != nil || !approx(got.UsedUSD, 123.4) || !approx(got.LimitUSD, 1000) || got.UsedPeriod != 0 || got.LimitReached ||
		len(got.Windows) != 1 || got.Windows[0].Name != "day" || !approx(got.Windows[0].UsedPct, 8.5) || !got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestMunitoShapes(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		body    string
		ok      bool
		used    float64
		period  time.Duration
		windows int
		reached bool
	}{
		// Only a day: its spend over the last day.
		{`{"periods":[{"period":"day","spent_usd":"3.5","limit_usd":35}]}`, true, 3.5, 24 * time.Hour, 1, false},
		// No limits: spend only, no window.
		{`{"periods":[{"period":"day","spent_usd":2,"limit_usd":null},{"period":"month","spent_usd":20,"limit_usd":0}]}`, true, 20, 0, 0, false},
		// Halted until later today.
		{`{"periods":[{"period":"week","spent_usd":1},{"period":"day","spent_usd":50,"limit_usd":50,"stopped_until":"2026-10-15T00:00:00Z"}]}`, true, 50, 24 * time.Hour, 1, true},
		// A halt that has ended is not one.
		{`{"periods":[{"period":"month","spent_usd":5,"limit_usd":5,"stopped_until":"2026-10-01T00:00:00Z"}]}`, true, 5, 0, 0, false},
		{`{"periods":[]}`, true, 0, 0, 0, false},
		{`{"periods":[{"period":"day","spent_usd":-1}]}`, false, 0, 0, 0, false},
		{`{"periods":[{"period":"day","spent_usd":"abc"}]}`, false, 0, 0, 0, false},
		{`{"periods":[{"period":"day","spent_usd":1,"stopped_until":"tomorrow"}]}`, false, 0, 0, 0, false},
		{`{}`, false, 0, 0, 0, false},
	} {
		srv := serve(t, "/admin/v1/limits", "k", tc.body)
		got, err := Munito{BaseURL: srv.URL, Now: func() time.Time { return now }}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (!approx(got.UsedUSD, tc.used) || got.UsedPeriod != tc.period || len(got.Windows) != tc.windows || got.LimitReached != tc.reached)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
