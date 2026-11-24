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

func openDesignServer(t *testing.T, current, plan string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "__Secure-better-auth.session_token=fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/workspaces/current":
			_, _ = w.Write([]byte(current))
		case strings.HasSuffix(r.URL.Path, "/billing/coding-plan-usage") && r.Header.Get("X-Vela-Workspace-Id") == "ws_fixture" &&
			r.URL.Path == "/api/v1/workspaces/ws_fixture/billing/coding-plan-usage":
			_, _ = w.Write([]byte(plan))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestOpenDesignFiveHourAndWeeklyWindows(t *testing.T) {
	srv := openDesignServer(t, `{"workspaceId":"ws_fixture","workspaceName":"Fixture"}`, fixture(t, "opendesign"))
	defer srv.Close()
	got, err := OpenDesign{BaseURL: srv.URL}.Read(context.Background(), "__Secure-better-auth.session_token=fixture")
	if err != nil || len(got.Windows) != 2 || !got.Subscription {
		t.Fatalf("%+v %v", got, err)
	}
	week, five := got.Windows[0], got.Windows[1]
	if week.Name != "week" || !approx(week.UsedPct, 6.967333) || !week.ResetsAt.Equal(time.Date(2026, 10, 28, 18, 29, 30, 0, time.UTC)) ||
		five.Name != "5h" || !approx(five.UsedPct, 1) || five.Duration != 5*time.Hour {
		t.Errorf("%+v", got.Windows)
	}
}

func TestOpenDesignShapes(t *testing.T) {
	const cur = `{"workspaceId":"ws_fixture"}`
	for _, tc := range []struct {
		name, current, plan string
		ok, auth            bool
	}{
		{"signed out", `{}`, `{}`, false, false},
		{"other workspace", cur, `{"workspaceId":"ws_other","eligible":true,"windows":[]}`, false, false},
		{"ineligible", cur, `{"workspaceId":"ws_fixture","eligible":false,"windows":[]}`, false, false},
		{"no known window", cur, `{"workspaceId":"ws_fixture","eligible":true,"windows":[{"durationSeconds":86400,"usedCredits":1,"limitCredits":2}]}`, false, false},
		{"duplicate", cur, `{"workspaceId":"ws_fixture","eligible":true,"windows":[{"durationSeconds":18000,"usedCredits":1,"limitCredits":2},{"durationSeconds":18000,"usedCredits":1,"limitCredits":2}]}`, false, false},
		{"zero limit", cur, `{"workspaceId":"ws_fixture","eligible":true,"windows":[{"durationSeconds":18000,"usedCredits":0,"limitCredits":0}]}`, false, false},
		{"numbers", cur, `{"workspaceId":"ws_fixture","eligible":true,"windows":[{"durationSeconds":18000,"usedCredits":0,"limitCredits":10}]}`, true, false},
	} {
		srv := openDesignServer(t, tc.current, tc.plan)
		_, err := OpenDesign{BaseURL: srv.URL}.Read(context.Background(), "__Secure-better-auth.session_token=fixture")
		srv.Close()
		if (err == nil) != tc.ok || errors.Is(err, usage.ErrAuth) != tc.auth {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	srv := openDesignServer(t, `{"workspaceId":"ws_fixture"}`, fixture(t, "opendesign"))
	defer srv.Close()
	if _, err := (OpenDesign{BaseURL: srv.URL}).Read(context.Background(), "__Secure-better-auth.session_token=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired: %v", err)
	}
}
