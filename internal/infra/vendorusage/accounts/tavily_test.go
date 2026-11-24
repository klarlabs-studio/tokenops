package accounts

import (
	"context"
	"testing"
	"time"
)

func TestTavilyPlanCreditsAreTheMonthWindow(t *testing.T) {
	srv := serve(t, "/usage", "tvly-key", fixture(t, "tavily"))
	defer srv.Close()
	got, err := Tavily{BaseURL: srv.URL}.Read(context.Background(), "tvly-key")
	if err != nil || !got.Subscription || len(got.Windows) != 1 || got.Windows[0].Name != "month" ||
		!approx(got.Windows[0].UsedPct, 50) || !got.Windows[0].ResetsAt.IsZero() || got.LimitReached {
		t.Fatalf("%+v %v", got, err)
	}
	if (Tavily{}).MinInterval() < time.Minute {
		t.Error("Tavily allows ten usage requests per ten minutes")
	}
}

func TestTavilyShapes(t *testing.T) {
	for _, tc := range []struct {
		body    string
		ok      bool
		windows int
		reached bool
	}{
		// An unlimited key, and a plan spent past its allowance.
		{`{"key":{"usage":5,"limit":null},"account":{"plan_usage":1500,"plan_limit":1000}}`, true, 1, false},
		// A zero allowance claims no percentage.
		{`{"key":{"usage":0,"limit":0},"account":{"plan_usage":0,"plan_limit":0}}`, true, 0, false},
		{`{"key":{"usage":200,"limit":200},"account":{"plan_usage":1,"plan_limit":10}}`, true, 1, true},
		{`{"key":{"usage":1},"account":{"plan_usage":1}}`, false, 0, false},
		{`{"key":{"usage":1.5,"limit":2},"account":{"plan_usage":1,"plan_limit":2}}`, false, 0, false},
		{`{"account":{"plan_usage":1,"plan_limit":2}}`, false, 0, false},
	} {
		srv := serve(t, "/usage", "k", tc.body)
		got, err := Tavily{BaseURL: srv.URL}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (len(got.Windows) != tc.windows || got.LimitReached != tc.reached)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
