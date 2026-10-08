package accounts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var ampNow = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func ampServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := io.ReadAll(r.Body)
		switch {
		case r.Header.Get("Authorization") == "Bearer expired":
			_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"auth-required","message":"Sign in"}}`))
		case r.Header.Get("Authorization") != "Bearer amp-token":
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method != http.MethodPost || r.URL.Path != "/api/internal" || r.URL.RawQuery != "userDisplayBalanceInfo" ||
			string(req) != `{"method":"userDisplayBalanceInfo","params":{}}`:
			w.WriteHeader(http.StatusBadRequest)
		default:
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The Tier's agent usage and orb hours are read from exact dollars and
// hours, for the billing period the text names.
func TestAmpReadsTheTier(t *testing.T) {
	srv := ampServer(t, fixture(t, "amp"))
	got, err := Amp{BaseURL: srv.URL, Now: func() time.Time { return ampNow }}.Read(context.Background(), "amp-token")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	end := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 7.15) || !w.ResetsAt.Equal(end) || w.Duration != 30*24*time.Hour {
		t.Errorf("agent %+v", w)
	}
	if w := got.Windows[1]; w.Name != "month (orb hours)" || !approx(w.UsedPct, 2.29333) {
		t.Errorf("orb %+v", w)
	}
	if !got.HasBalance || got.BalanceUSD != 20 {
		t.Errorf("credits %+v", got)
	}
}

func TestAmpRefusedToken(t *testing.T) {
	srv := ampServer(t, fixture(t, "amp"))
	for _, key := range []string{"expired", "other"} {
		if _, err := (Amp{BaseURL: srv.URL}).Read(context.Background(), key); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s: %v", key, err)
		}
	}
}

func TestAmpCLIReadsTheSameText(t *testing.T) {
	const free = "\x1b[2mSigned in as ampcode@3kh0.net (echo)\x1b[0m\n" +
		"Amp Free: $4.71/$10 remaining (replenishes +$0.42/hour) - https://ampcode.com/settings#amp-free\n" +
		"Individual credits: $25.64 remaining (set up automatic top-up to avoid running out) - https://ampcode.com/settings\n" +
		"Workspace meow: $10.22 remaining (set up automatic top-up to avoid running out) - https://ampcode.com/workspaces/meow\n"
	bin, log := fakeCLI(t, map[string]fakeAnswer{"usage": ok(free)})
	got, err := AmpCLI{Bin: bin, Now: func() time.Time { return ampNow }}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 1 || got.BalanceUSD != 25.64 {
		t.Fatalf("got %+v, %v", got, err)
	}
	full := ampNow.Add(12*time.Hour + 35*time.Minute) // $5.29 used, replenished at $0.42 an hour
	if w := got.Windows[0]; w.Name != "day" || !approx(w.UsedPct, 52.9) || w.Duration != 24*time.Hour ||
		w.ResetsAt.Sub(full).Abs() > time.Minute {
		t.Errorf("free %+v", w)
	}
	if calls := invocations(t, log); len(calls) != 1 || calls[0] != "usage" {
		t.Errorf("ran %q", calls)
	}
}

func TestAmpCLISignedOut(t *testing.T) {
	bin, _ := fakeCLI(t, map[string]fakeAnswer{"usage": failed("Please log in: run amp login\n")})
	if _, err := (AmpCLI{Bin: bin}).Read(context.Background(), ""); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AMP_CLI_PATH", "")
	if _, err := (AmpCLI{}).Read(context.Background(), ""); !errors.Is(err, usage.ErrNotInstalled) {
		t.Errorf("not installed: %v", err)
	}
}

func TestAmpDisplayShapes(t *testing.T) {
	now := time.Date(2026, 8, 3, 22, 0, 0, 0, time.UTC)
	legacy := "Signed in as fixture@example.test (example)\n" +
		"Amp Free: 61% remaining today (resets daily) - https://ampcode.com/settings#amp-free\n" +
		"Subscription Gigawatt: 73% other usage and 91% orb usage remaining - resets upon renewal in 1 month\n" +
		"Individual credits: $17.23 remaining (set up auto-reload to avoid running out) - https://ampcode.com/settings\n"
	got, err := parseAmpDisplay(legacy, now)
	if err != nil || len(got.Windows) != 3 {
		t.Fatalf("legacy %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month" || w.UsedPct != 27 || !w.ResetsAt.Equal(time.Date(2026, 9, 3, 22, 0, 0, 0, time.UTC)) {
		t.Errorf("other %+v", w)
	}
	if w := got.Windows[1]; w.UsedPct != 9 {
		t.Errorf("orb %+v", w)
	}
	if w := got.Windows[2]; w.Name != "day" || w.UsedPct != 39 || !w.ResetsAt.Equal(time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("free %+v", w)
	}
	bold := "Signed in as a@b.c\n**Amp Megawatt Subscription:** 68% other usage and 97% orb usage remaining - resets upon renewal in 5 days"
	if got, err := parseAmpDisplay(bold, now); err != nil || len(got.Windows) != 2 || got.Windows[0].UsedPct != 32 || got.Windows[1].UsedPct != 3 {
		t.Errorf("bold %+v, %v", got, err)
	}
	if _, err := parseAmpDisplay("Signed in as a@b.c\nnothing else", now); err == nil {
		t.Error("no usage parsed without error")
	}
	if _, err := parseAmpDisplay("Visit https://ampcode.com/login to sign in", now); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("signed out: %v", err)
	}
}
