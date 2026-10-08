package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var xkiroNow = func() time.Time { return time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC) }

func TestXKiroReadsWindowsFreeTokensAndWallet(t *testing.T) {
	srv := serve(t, "/v1/usage", "xk", fixture(t, "xkiro"))
	defer srv.Close()
	got, err := XKiro{BaseURL: srv.URL, Now: xkiroNow}.Read(context.Background(), "xk")
	if err != nil || !got.Subscription || len(got.Windows) != 3 || !got.HasBalance || !approx(got.BalanceUSD, 683.95) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.Date(2026, 10, 8, 10, 53, 0, 0, time.UTC)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.Duration != 7*24*time.Hour {
		t.Errorf("week %+v", w)
	}
	// 412,030 of 300,000,000 free tokens; resets at the next 00:00 UTC.
	if w := got.Windows[2]; w.Name != "free tokens" || !approx(w.UsedPct, 0.1373) || !w.ResetsAt.Equal(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("free tokens %+v", w)
	}
}

func TestXKiroPayAsYouGoAndNoCap(t *testing.T) {
	srv := serve(t, "/v1/usage", "xk", `{"object":"usage","plan":null,"windows":[],"free_tokens":{"used_today":10,"limit_per_day":null,"remaining":null},"wallet":null}`)
	defer srv.Close()
	got, err := XKiro{BaseURL: srv.URL, Now: xkiroNow}.Read(context.Background(), "xk")
	if err != nil || got.Subscription || len(got.Windows) != 0 || !got.Empty() {
		t.Errorf("pay-as-you-go without a cap = %+v, %v", got, err)
	}
	unknown := serve(t, "/v1/usage", "xk", `{"object":"list"}`)
	defer unknown.Close()
	if _, err := (XKiro{BaseURL: unknown.URL}).Read(context.Background(), "xk"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unknown shape = %v", err)
	}
	if _, err := (XKiro{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}
