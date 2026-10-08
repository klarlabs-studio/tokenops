package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func sakanaServer(t *testing.T, refuse int, billing, payg string) *webServer {
	return newWebServer(t, "session=fixture", refuse, map[string]string{
		"/billing?":               billing,
		"/billing?tab=payAsYouGo": payg,
	})
}

func TestSakanaReadsTheBillingPage(t *testing.T) {
	srv := sakanaServer(t, http.StatusUnauthorized, fixturePart(t, "sakana", "billing"), fixturePart(t, "sakana", "payg"))
	got, err := Sakana{BaseURL: srv.URL}.Read(context.Background(), "session=fixture")
	if err != nil || len(got.Windows) != 2 || !got.HasBalance || !approx(got.BalanceUSD, 12.34) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 92 || !w.ResetsAt.Equal(time.Date(2026, 6, 23, 14, 53, 0, 0, time.UTC)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.UsedPct != 32 || !w.ResetsAt.Equal(time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week %+v", w)
	}
}

// The pay-as-you-go tab is best-effort: without it the windows stand.
func TestSakanaWithoutThePayAsYouGoTab(t *testing.T) {
	srv := newWebServer(t, "session=fixture", http.StatusUnauthorized, map[string]string{"/billing?": fixturePart(t, "sakana", "billing")})
	got, err := Sakana{BaseURL: srv.URL}.Read(context.Background(), "session=fixture")
	if err != nil || len(got.Windows) != 2 || got.HasBalance {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestSakanaRefusedOrUnreadable(t *testing.T) {
	srv := sakanaServer(t, http.StatusFound, "", "")
	if _, err := (Sakana{BaseURL: srv.URL}).Read(context.Background(), "session=expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("redirect to sign-in = %v", err)
	}
	signIn := sakanaServer(t, 0, `<html><form>Sign in to continue</form></html>`, "")
	if _, err := (Sakana{BaseURL: signIn.URL}).Read(context.Background(), "session=fixture"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a sign-in page = %v", err)
	}
	odd := sakanaServer(t, 0, `<main><p>5-hour</p><p>lots used</p></main>`, "")
	if _, err := (Sakana{BaseURL: odd.URL}).Read(context.Background(), "session=fixture"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unreadable window = %v", err)
	}
}
