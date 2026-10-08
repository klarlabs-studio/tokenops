package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// zedServer answers like cloud.zed.dev: the sign-in is "<user id> <token>"
// with no scheme.
func zedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "4242 fixture-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/client/users/me" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestZedReadsEditPredictions(t *testing.T) {
	srv := zedServer(t, fixture(t, "zed"))
	got, err := Zed{BaseURL: srv.URL}.Read(context.Background(), "4242 fixture-token")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month (edit predictions)" || w.UsedPct != 50 ||
		!w.ResetsAt.Equal(time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)) || w.Duration != 31*24*time.Hour {
		t.Errorf("window %+v", w)
	}
}

func TestZedRefusedSignIn(t *testing.T) {
	srv := zedServer(t, fixture(t, "zed"))
	for _, key := range []string{"4242 expired", "no-space", ""} {
		if _, err := (Zed{BaseURL: srv.URL}).Read(context.Background(), key); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", key, err)
		}
	}
}

func TestZedShapes(t *testing.T) {
	const tmpl = `{"user":{"id":42,"github_login":"fixture"},"plan":{"plan_v3":"zed_pro","has_overdue_invoices":false,
  "usage":{"edit_predictions":{"used":0,"limit":LIMIT}},
  "subscription_period":{"started_at":"2026-05-13T00:00:00Z","ended_at":"2026-06-13T00:00:00Z"}}}`
	for _, tc := range []struct {
		limit   string
		windows int
	}{{`"unlimited"`, 0}, {`2000`, 1}, {`{"limited":2000}`, 1}, {`0`, 0}} {
		srv := zedServer(t, strings.Replace(tmpl, "LIMIT", tc.limit, 1))
		got, err := Zed{BaseURL: srv.URL}.Read(context.Background(), "4242 fixture-token")
		if err != nil || len(got.Windows) != tc.windows {
			t.Errorf("limit %s: %+v, %v", tc.limit, got, err)
		}
	}
	for _, bad := range []string{`{}`, `[]`, `{"plan":{}}`} {
		srv := zedServer(t, bad)
		if _, err := (Zed{BaseURL: srv.URL}).Read(context.Background(), "4242 fixture-token"); err == nil {
			t.Errorf("%s read without error", bad)
		}
	}
}
