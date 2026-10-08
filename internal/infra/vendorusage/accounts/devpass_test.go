package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestDevPassReadsThePlanWindows(t *testing.T) {
	srv := serve(t, "/v1/key", "dp", fixture(t, "devpass"))
	defer srv.Close()
	got, err := DevPass{BaseURL: srv.URL}.Read(context.Background(), "dp")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 25.0/237*100) || !w.ResetsAt.IsZero() {
		t.Errorf("cycle %+v", w)
	}
	if w := got.Windows[1]; w.Name != "premium week" || !approx(w.UsedPct, 5/35.55*100) || w.Duration != 7*24*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("premium week %+v", w)
	}
}

func TestDevPassPayAsYouGoAndUnknown(t *testing.T) {
	srv := serve(t, "/v1/key", "dp", `{"data":{"usage":"12.50","limit":"10","devPlan":"none","devPlanCreditsUsed":"0","devPlanCreditsLimit":"0","devPlanCreditsRemaining":"0","devPlanPremiumWeeklyLimit":"0","devPlanPremiumCreditsUsed":"0","devPlanPremiumWeekResetsAt":null}}`)
	defer srv.Close()
	got, err := DevPass{BaseURL: srv.URL}.Read(context.Background(), "dp")
	if err != nil || got.Subscription || !got.HasUsed || !approx(got.UsedUSD, 12.5) || got.LimitUSD != 10 || !got.LimitReached || got.Scope != "key" {
		t.Errorf("pay-as-you-go = %+v, %v", got, err)
	}
	unknown := serve(t, "/v1/key", "dp", `{"data":{"devPlan":"enterprise"}}`)
	defer unknown.Close()
	if _, err := (DevPass{BaseURL: unknown.URL}).Read(context.Background(), "dp"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unknown plan = %v", err)
	}
	if _, err := (DevPass{BaseURL: srv.URL}).Read(context.Background(), "publishable"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}
