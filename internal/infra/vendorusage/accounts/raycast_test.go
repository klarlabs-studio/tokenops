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

// raycastServer answers the credits call for the session "rs", and fails
// a request that carries any cookie but the two it needs.
func raycastServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := r.Header.Get("Cookie")
		if c != "__raycast_session=rs" && c != "__raycast_session=rs; csrf_token=ct" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/frontend_api/current_user/ai_credits" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRaycastReadsTheCredits(t *testing.T) {
	srv := raycastServer(t, fixture(t, "raycast"))
	for _, cred := range []string{"_ga=1; __raycast_session=rs; csrf_token=ct", "rs"} {
		got, err := Raycast{BaseURL: srv.URL}.Read(context.Background(), cred)
		if err != nil || !got.Subscription || len(got.Windows) != 1 {
			t.Fatalf("%q: %+v, %v", cred, got, err)
		}
		if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 32.524) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("month %+v", w)
		}
		if !got.HasCredits || !approx(got.Credits, 337.38) || got.CreditsUnit != "credits" {
			t.Errorf("credits %+v", got)
		}
	}
}

// A rollover above the grant is 0% used; a zero allowance has no window.
func TestRaycastShapes(t *testing.T) {
	over := raycastServer(t, `{"remaining_balance_credits":750,"total_balance_credits":500}`)
	if got, err := (Raycast{BaseURL: over.URL}).Read(context.Background(), "rs"); err != nil || got.Windows[0].UsedPct != 0 {
		t.Errorf("rollover %+v, %v", got, err)
	}
	zero := raycastServer(t, `{"remaining_balance_credits":0,"total_balance_credits":0}`)
	if got, err := (Raycast{BaseURL: zero.URL}).Read(context.Background(), "rs"); err != nil || len(got.Windows) != 0 || !got.HasCredits {
		t.Errorf("zero %+v, %v", got, err)
	}
	unknown := raycastServer(t, `{"error":"x"}`)
	if got, err := (Raycast{BaseURL: unknown.URL}).Read(context.Background(), "rs"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown %+v, %v", got, err)
	}
}

func TestRaycastRefusals(t *testing.T) {
	srv := raycastServer(t, fixture(t, "raycast"))
	for _, cred := range []string{"__raycast_session=old", "csrf_token=ct"} {
		if _, err := (Raycast{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
}
