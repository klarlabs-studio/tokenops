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

// headerServer answers path with body when every header in want is set as
// given, and 401 otherwise.
func headerServer(t *testing.T, method, path string, want map[string]string, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range want {
			if r.Header.Get(k) != v {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		if r.Method != method || r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMuseReadsTheSubscriptionWindows(t *testing.T) {
	srv := headerServer(t, http.MethodPost, "/muse-code/key",
		map[string]string{"Authorization": "Bearer dca:tok", "x-api-version": "1.0.0"}, fixture(t, "muse"))
	got, err := Muse{BaseURL: srv.URL}.Read(context.Background(), "dca:tok")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 37.5) || !w.ResetsAt.Equal(time.Unix(1793491200, 0)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 12) {
		t.Errorf("week %+v", w)
	}
	for _, tok := range []string{"dca:bad", "not-a-muse-token"} {
		if _, err := (Muse{BaseURL: srv.URL}).Read(context.Background(), tok); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", tok, err)
		}
	}
}

func TestMuseIdleOrUnsubscribedIsEmpty(t *testing.T) {
	for _, body := range []string{`{"is_subs_active":false}`, `{"is_subs_active":true,"subs_usage":null}`} {
		srv := headerServer(t, http.MethodPost, "/muse-code/key", nil, body)
		got, err := Muse{BaseURL: srv.URL}.Read(context.Background(), "dca:t")
		if err != nil || !got.Empty() {
			t.Errorf("%s: %+v %v", body, got, err)
		}
	}
}

func TestFactoryReadsTheRateLimits(t *testing.T) {
	now := time.Date(2026, 10, 8, 19, 0, 0, 0, time.UTC)
	srv := headerServer(t, http.MethodGet, "/api/billing/limits",
		map[string]string{"Authorization": "Bearer fk", "x-factory-client": "web-app"}, fixture(t, "factory"))
	got, err := Factory{BaseURL: srv.URL, Now: func() time.Time { return now }}.Read(context.Background(), "fk")
	if err != nil || !got.Subscription || len(got.Windows) != 3 || !got.HasBalance || !approx(got.BalanceUSD, 12.5) {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 40) || !w.ResetsAt.Equal(now.Add(time.Hour)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !w.ResetsAt.Equal(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week %+v", w)
	}
	// The month ended with nothing remaining: it rolled over, unused.
	if w := got.Windows[2]; w.Name != "month" || w.UsedPct != 0 {
		t.Errorf("month %+v", w)
	}
	if _, err := (Factory{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused key: %v", err)
	}
}

func TestGrokReadsTheBillingPeriod(t *testing.T) {
	srv := headerServer(t, http.MethodGet, "/v1/billing",
		map[string]string{"Authorization": "Bearer gk", "x-xai-token-auth": "xai-grok-cli"}, fixture(t, "grok"))
	got, err := Grok{BaseURL: srv.URL}.Read(context.Background(), "gk")
	if err != nil || !got.Subscription || len(got.Windows) != 1 || !got.HasBalance || !approx(got.BalanceUSD, 19.99) {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 62.5) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window %+v", w)
	}
	// With no reported percentage, on-demand use against its cap.
	srv2 := headerServer(t, http.MethodGet, "/v1/billing", nil, `{"config":{"onDemandCap":{"val":200},"onDemandUsed":{"val":"50"},"billingPeriodEnd":"2026-10-15T00:00:00Z"}}`)
	got, err = Grok{BaseURL: srv2.URL}.Read(context.Background(), "gk")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 25) {
		t.Errorf("%+v %v", got, err)
	}
	if _, err := (Grok{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused: %v", err)
	}
}

// Codebuff with the CLI's session also reads the weekly rate limit, which
// its API key cannot.
func TestCodebuffAppLoginReadsTheWeeklyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sess" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/usage":
			_, _ = w.Write([]byte(fixture(t, "codebuff")))
		case "/api/user/subscription":
			_, _ = w.Write([]byte(`{"hasSubscription":true,"subscription":{"status":"active","tier":"pro"},"rateLimit":{"weeklyUsed":2100,"weeklyLimit":7000,"weeklyResetsAt":"2026-05-08T00:00:00Z"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	got, err := Codebuff{BaseURL: srv.URL}.ReadAppLogin(context.Background(), "sess")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 30) {
		t.Errorf("week %+v", w)
	}
}

// ClinePass sends Cline's own sign-in as a WorkOS token.
func TestClinePassAppLoginIsAWorkOSToken(t *testing.T) {
	srv := serve(t, "/api/v1/users/me/plan/usage-limits", "workos:at", fixture(t, "clinepass"))
	defer srv.Close()
	for _, tok := range []string{"at", "workos:at"} {
		if got, err := (ClinePass{BaseURL: srv.URL}).ReadAppLogin(context.Background(), tok); err != nil || len(got.Windows) == 0 {
			t.Errorf("%q: %+v %v", tok, got, err)
		}
	}
}
