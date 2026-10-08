package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const auggieStatus = `╭ Account ───────────────────────────────────────────────╮
│                                                        │
│ 319,054 credits remaining                     Max Plan │
│                                450,000 credits / month │
│                                                        │
╰────────────────────────────────────────────────────────╯

 9 days remaining in this billing cycle (ends 6/9/2026)
 For more detail, visit https://app.augmentcode.com/account
`

func TestAugmentCLIReadsTheAccountBox(t *testing.T) {
	bin, log := fakeCLI(t, map[string]fakeAnswer{"account": ok(auggieStatus)})
	got, err := AugmentCLI{Bin: bin}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 130946.0/450000*100) ||
		!w.ResetsAt.Equal(time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)) {
		t.Errorf("window %+v", w)
	}
	if calls := invocations(t, log); len(calls) != 1 || calls[0] != "account status" {
		t.Errorf("ran %q", calls)
	}
	legacy := "Max Plan 450,000 credits / month\n11,657 remaining · 953,170 / 964,827 credits used\n2 days remaining in this billing cycle (ends 1/8/2026)\n"
	if got, err := parseAuggieStatus(legacy); err != nil || !approx(got.Windows[0].UsedPct, 953170.0/964827*100) {
		t.Errorf("legacy %+v, %v", got, err)
	}
	if _, err := parseAuggieStatus("Welcome to Auggie"); err == nil {
		t.Error("nothing parsed without error")
	}
}

func TestAugmentCLISignedOut(t *testing.T) {
	bin, _ := fakeCLI(t, map[string]fakeAnswer{"account": failed("Authentication failed. Run auggie login.\n")})
	if _, err := (AugmentCLI{Bin: bin}).Read(context.Background(), ""); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AUGGIE_CLI_PATH", "")
	if _, err := (AugmentCLI{}).Read(context.Background(), ""); !errors.Is(err, usage.ErrNotInstalled) {
		t.Errorf("not installed: %v", err)
	}
}

func augmentServer(t *testing.T, credits string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "_session=s1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/credits":
			_, _ = w.Write([]byte(credits))
		case "/api/subscription":
			_, _ = w.Write([]byte(`{"planName":"Developer","billingPeriodEnd":"2026-11-01T00:00:00.000Z","email":"a@b.c"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAugmentWebReadsCredits(t *testing.T) {
	srv := augmentServer(t, fixture(t, "augment"))
	got, err := Augment{BaseURL: srv.URL}.Read(context.Background(), "_session=s1")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.UsedPct != 10 || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window %+v", w)
	}
	// Without an allowance, the limit is what is left plus what was used.
	noLimit := augmentServer(t, `{"usageUnitsRemaining": 15, "usageUnitsConsumedThisBillingCycle": 10, "usageUnitsAvailable": 0}`)
	if got, err := (Augment{BaseURL: noLimit.URL}).Read(context.Background(), "_session=s1"); err != nil || got.Windows[0].UsedPct != 40 {
		t.Errorf("no allowance %+v, %v", got, err)
	}
	if _, err := (Augment{BaseURL: srv.URL}).Read(context.Background(), "_session=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired: %v", err)
	}
	empty := augmentServer(t, `{}`)
	if _, err := (Augment{BaseURL: empty.URL}).Read(context.Background(), "_session=s1"); err == nil {
		t.Error("an empty answer read without error")
	}
}
