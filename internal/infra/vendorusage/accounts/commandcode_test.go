package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const ccCookie = "__Secure-commandcode_prod_.session_token=tok"

func TestCommandCodeReadsWindowsAndTheMonth(t *testing.T) {
	srv := newWebServer(t, ccCookie, http.StatusUnauthorized, routesOf(t, "commandcode"))
	// A bare token is the production session cookie's value.
	got, err := CommandCode{BaseURL: srv.URL}.Read(context.Background(), "tok")
	if err != nil || len(got.Windows) != 3 || !got.HasBalance || got.BalanceUSD != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.UnixMilli(1780000000000).UTC()) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 10) {
		t.Errorf("week %+v", w)
	}
	// $8.50 left of the Go plan's $10 grant.
	if w := got.Windows[2]; w.Name != "month" || !approx(w.UsedPct, 15) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month %+v", w)
	}
}

// The grant the credits call reports wins over the plan's; without the
// subscription call the month has no reset.
func TestCommandCodeNestedLimitsAndReportedGrant(t *testing.T) {
	routes := map[string]string{"/internal/billing/credits": `{"credits":{"monthlyCredits":7.25,"purchasedCredits":0,"monthlyCreditsGranted":29,
		"windowLimits":{"fiveHour":{"cap":"4","used":"1","resetAt":"1780200000"},"weekly":{"cap":20,"used":4,"resetAt":1780300000000}}}}`}
	srv := newWebServer(t, ccCookie, http.StatusUnauthorized, routes)
	got, err := CommandCode{BaseURL: srv.URL}.Read(context.Background(), ccCookie)
	if err != nil || len(got.Windows) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.Unix(1780200000, 0).UTC()) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[2]; !approx(w.UsedPct, 75) || !w.ResetsAt.IsZero() {
		t.Errorf("month %+v", w)
	}
}

func TestCommandCodeRefusalsAndShape(t *testing.T) {
	srv := newWebServer(t, ccCookie, http.StatusUnauthorized, routesOf(t, "commandcode"))
	if _, err := (CommandCode{BaseURL: srv.URL}).Read(context.Background(), "expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused = %v", err)
	}
	odd := newWebServer(t, "", 0, map[string]string{"/internal/billing/credits": `{"credits":{}}`})
	if _, err := (CommandCode{BaseURL: odd.URL}).Read(context.Background(), ccCookie); err == nil {
		t.Error("an answer without credits was read")
	}
}
