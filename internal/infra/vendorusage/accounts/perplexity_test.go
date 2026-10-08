package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// perplexityServer answers the credits call for the session "sess" under
// cookie, and counts the requests.
func perplexityServer(t *testing.T, cookie, body string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != cookie+"=sess" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/rest/billing/credits" || r.URL.Query().Get("version") == "" || r.Header.Get("User-Agent") == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPerplexityReadsTheCredits(t *testing.T) {
	var calls atomic.Int32
	srv := perplexityServer(t, "__Secure-next-auth.session-token", fixture(t, "perplexity"), &calls)
	got, err := Perplexity{BaseURL: srv.URL}.Read(context.Background(), "other=1; __Secure-next-auth.session-token=sess")
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("%+v, %v", got, err)
	}
	if !got.HasBalance || !approx(got.BalanceUSD, 12.50) || got.HasCredits {
		t.Errorf("balance %+v", got)
	}
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 75) ||
		!w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month %+v", w)
	}
}

// A bare session value is tried under each cookie name Perplexity has
// used; a session split into numbered chunks is joined.
func TestPerplexityCookieForms(t *testing.T) {
	var calls atomic.Int32
	srv := perplexityServer(t, "__Secure-authjs.session-token", fixture(t, "perplexity"), &calls)
	if _, err := (Perplexity{BaseURL: srv.URL}).Read(context.Background(), "sess"); err != nil || calls.Load() != 2 {
		t.Errorf("bare value: %v after %d calls", err, calls.Load())
	}
	if _, err := (Perplexity{BaseURL: srv.URL}).Read(context.Background(),
		"__Secure-authjs.session-token.1=ss; __Secure-authjs.session-token.0=se"); err != nil {
		t.Errorf("chunked: %v", err)
	}
}

// An expired session is refused; an API key and a header with no session
// are refused without a request.
func TestPerplexityRefusals(t *testing.T) {
	var calls atomic.Int32
	srv := perplexityServer(t, "__Secure-next-auth.session-token", fixture(t, "perplexity"), &calls)
	if _, err := (Perplexity{BaseURL: srv.URL}).Read(context.Background(), "__Secure-next-auth.session-token=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired session = %v", err)
	}
	calls.Store(0)
	for _, cred := range []string{"pplx-abc123", "theme=dark"} {
		if _, err := (Perplexity{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v", cred, err)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("%d requests made without a session", calls.Load())
	}
}

// An answer without the credit fields is an error; an account with no
// recurring grant has a balance and no window.
func TestPerplexityShapes(t *testing.T) {
	var calls atomic.Int32
	bad := perplexityServer(t, "__Secure-next-auth.session-token", `{"error":"nope"}`, &calls)
	if got, err := (Perplexity{BaseURL: bad.URL}).Read(context.Background(), "__Secure-next-auth.session-token=sess"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown shape: %+v, %v", got, err)
	}
	free := perplexityServer(t, "__Secure-next-auth.session-token",
		strings.ReplaceAll(fixture(t, "perplexity"), `"recurring"`, `"promotional"`), &calls)
	got, err := Perplexity{BaseURL: free.URL}.Read(context.Background(), "__Secure-next-auth.session-token=sess")
	if err != nil || got.Subscription || len(got.Windows) != 0 || !got.HasBalance {
		t.Errorf("no plan: %+v, %v", got, err)
	}
}
