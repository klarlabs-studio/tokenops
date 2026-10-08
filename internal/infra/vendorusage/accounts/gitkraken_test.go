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

// gitkrakenServer answers like api.gitkraken.dev: 401 for another token,
// and the organization pool only when gk-org-id names it.
func gitkrakenServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gk-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v1/ai-tasks/usage" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("gk-org-id") != "org-fixture" {
			_, _ = w.Write([]byte(`{"data":{"used":12500,"limit":400000,"resetsOn":"2026-09-27T00:00:00Z"},"error":null}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitKrakenReadsWeeklyCredits(t *testing.T) {
	srv := gitkrakenServer(t, fixture(t, "gitkraken"))
	got, err := GitKraken{BaseURL: srv.URL, OrgID: "org-fixture"}.Read(context.Background(), "gk-token")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	reset := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	if w := got.Windows[0]; w.Name != "week" || !approx(w.UsedPct, 3.125) || w.Duration != 7*24*time.Hour || !w.ResetsAt.Equal(reset) {
		t.Errorf("personal %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week (organization)" || !approx(w.UsedPct, 20) {
		t.Errorf("organization %+v", w)
	}
	// Without an organization only the personal quota is read.
	personal, err := GitKraken{BaseURL: srv.URL, OrgID: ""}.Read(context.Background(), "gk-token")
	if err != nil || len(personal.Windows) != 1 {
		t.Errorf("personal only: %+v, %v", personal, err)
	}
}

func TestGitKrakenRefusedTokenIsErrAuth(t *testing.T) {
	srv := gitkrakenServer(t, fixture(t, "gitkraken"))
	if _, err := (GitKraken{BaseURL: srv.URL}).Read(context.Background(), "expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

// No allowance (0) and unlimited (-1) have no percentage; an unknown or
// invalid shape is an error, never a zero.
func TestGitKrakenShapes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		windows    int
		fails      bool
	}{
		{"no allowance", `{"data":{"used":12500,"limit":0,"resetsOn":"2026-09-27T00:00:00Z"},"error":null}`, 0, false},
		{"unlimited", `{"data":{"used":12500,"limit":-1,"resetsOn":"2026-09-27T00:00:00Z"},"error":null}`, 0, false},
		{"bad organization ignored", `{"data":{"used":1,"limit":100,"resetsOn":"2026-09-27T00:00:00Z","organization":{"used":-1,"limit":5}},"error":null}`, 1, false},
		{"empty", `{}`, 0, true},
		{"negative used", `{"data":{"used":-1,"limit":100},"error":null}`, 0, true},
		{"bad limit", `{"data":{"used":1,"limit":-0.5},"error":null}`, 0, true},
		{"error set", `{"data":{"used":1,"limit":100},"error":"private-response-fixture"}`, 0, true},
		{"not json", `private-response-fixture`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			got, err := GitKraken{BaseURL: srv.URL, OrgID: "org"}.Read(context.Background(), "k")
			if (err != nil) != tc.fails || len(got.Windows) != tc.windows {
				t.Errorf("got %+v, %v", got, err)
			}
		})
	}
	// A date without a zone is not a reset time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"used":1,"limit":100,"resetsOn":"2026-09-27"},"error":null}`))
	}))
	defer srv.Close()
	got, err := GitKraken{BaseURL: srv.URL}.Read(context.Background(), "k")
	if err != nil || len(got.Windows) != 1 || !got.Windows[0].ResetsAt.IsZero() {
		t.Errorf("date-only reset: %+v, %v", got, err)
	}
}
