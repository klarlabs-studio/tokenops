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

// zoomMateServer mints the token "nak1" for the cookie "zs=ok" and serves
// the credit status to it, as ZoomMate's web API does.
func zoomMateServer(t *testing.T, status string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ai-computer/api/v1/login/":
			if r.Header.Get("Cookie") != "zs=ok" || r.URL.Query().Get("continue") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"nak":"nak1","user_profile":{"email":"a@b.c"}}}`))
		case "/ai-computer/api/v1/credits/status":
			if r.Header.Get("Authorization") != "Bearer nak1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(status))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestZoomMateReadsTheCredits(t *testing.T) {
	srv := zoomMateServer(t, fixture(t, "zoommate"))
	for _, cred := range []string{"zs=ok", "Cookie: zs=ok", "Bearer nak1"} {
		got, err := ZoomMate{BaseURL: srv.URL}.Read(context.Background(), cred)
		if err != nil || !got.Subscription || len(got.Windows) != 1 {
			t.Fatalf("%q: %+v, %v", cred, got, err)
		}
		if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 25) || w.Duration != 31*24*time.Hour ||
			!w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("window %+v", w)
		}
		if !got.HasCredits || got.Credits != 1500 || got.CreditsUnit != "credits" {
			t.Errorf("credits %+v", got)
		}
	}
}

func TestZoomMateRefusals(t *testing.T) {
	srv := zoomMateServer(t, fixture(t, "zoommate"))
	for _, cred := range []string{"zs=expired", "Bearer stale", "nothing"} {
		if _, err := (ZoomMate{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
}

func TestZoomMateShapes(t *testing.T) {
	unlimited := zoomMateServer(t, `{"data":{"credit_status":{"budget_cap":0,"used_credit":12,"is_unlimited":true}}}`)
	if got, err := (ZoomMate{BaseURL: unlimited.URL}).Read(context.Background(), "zs=ok"); err != nil || !got.Empty() {
		t.Errorf("unlimited = %+v, %v", got, err)
	}
	unknown := zoomMateServer(t, `{"data":{}}`)
	if got, err := (ZoomMate{BaseURL: unknown.URL}).Read(context.Background(), "zs=ok"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("unknown = %+v, %v", got, err)
	}
}
