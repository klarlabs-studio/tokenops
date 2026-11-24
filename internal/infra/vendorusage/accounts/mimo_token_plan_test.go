package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// CodexBar's TestsPlugin/MiMoTokenPlanPluginTests.swift fixture.
const mimoTokenPlan = `{"object":"token_plan","plan_code":"lite","period":{"start_at":"2026-11-11 21:05:27","end_at":"2026-12-11 23:59:59"},"quota":{"total_credits":4100000000,"used_credits":1400000000,"remaining_credits":2700000000}}`

func TestMiMoTokenPlanCreditsOverThePeriod(t *testing.T) {
	srv := serve(t, "/v1/platform/token_plan", "tp-key", mimoTokenPlan)
	defer srv.Close()
	got, err := MiMoTokenPlan{BaseURL: srv.URL}.Read(context.Background(), "sgp:tp-key")
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	end := time.Date(2026, 12, 11, 23, 59, 59, 0, time.UTC)
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 34.146341) || !w.ResetsAt.Equal(end) ||
		w.Duration != end.Sub(time.Date(2026, 11, 11, 21, 5, 27, 0, time.UTC)) {
		t.Errorf("%+v", w)
	}
	if _, err := (MiMoTokenPlan{BaseURL: srv.URL}).Read(context.Background(), "api-platform_serviceToken=x; userId=1"); !errors.Is(err, usage.ErrSkip) {
		t.Errorf("a session: %v", err)
	}
}

func TestMiMoTokenPlanShapes(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		windows int
	}{
		{`{"object":"token_plan","quota":{"total_credits":0,"used_credits":0,"remaining_credits":0}}`, true, 0},
		{`{"object":"token_plan","quota":{"total_credits":10,"used_credits":4,"remaining_credits":5}}`, false, 0},
		{`{"object":"token_plan","quota":{"total_credits":10,"used_credits":1.5,"remaining_credits":8.5}}`, false, 0},
		{`{"object":"token_plan","period":{"start_at":"2026-12-11T00:00:00Z"},"quota":{"total_credits":10,"used_credits":1,"remaining_credits":9}}`, false, 0},
		{`{"object":"balance"}`, false, 0},
	} {
		srv := serve(t, "/v1/platform/token_plan", "k", tc.body)
		got, err := MiMoTokenPlan{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && len(got.Windows) != tc.windows) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
