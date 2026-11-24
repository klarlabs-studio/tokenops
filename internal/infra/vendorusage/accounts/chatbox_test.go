package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var chatboxNow = func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }

func TestChatboxPlanAndTopUpPools(t *testing.T) {
	srv := serveRaw(t, "/api/license/detail/realtime", "lic-fixture", fixture(t, "chatbox"))
	defer srv.Close()
	got, err := Chatbox{BaseURL: srv.URL, Now: chatboxNow}.Read(context.Background(), "lic-fixture")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	plan, topup := got.Windows[0], got.Windows[1]
	if plan.Name != "plan" || !approx(plan.UsedPct, 20) || !plan.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) ||
		topup.Name != "top-up credits" || !approx(topup.UsedPct, 12.5) || !topup.ResetsAt.IsZero() {
		t.Errorf("%+v", got.Windows)
	}
	if _, err := (Chatbox{}).Read(context.Background(), "two words"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("spaces: %v", err)
	}
}

func TestChatboxShapes(t *testing.T) {
	plan := `"unified_token_usage_details":[{"type":"plan","token_usage":10,"token_limit":100}]`
	for _, tc := range []struct {
		name, body string
		ok         bool
		windows    int
		reset      bool
	}{
		// An expired plan has no meter; a refresh after expiry is no reset.
		{"expired", `{"data":{` + plan + `,"token_expire_time":"2026-10-01T00:00:00Z"}}`, true, 0, false},
		{"refresh after expiry", `{"data":{` + plan + `,"token_next_refresh_time":"2026-12-01T00:00:00Z","expires_at":"2026-11-15T00:00:00Z"}}`, true, 1, false},
		{"inactive", `{"data":{` + plan + `,"status":"suspended"}}`, true, 0, false},
		{"combined only", `{"data":{"unified_token_usage":5,"unified_token_limit":50}}`, true, 0, false},
		{"two plans", `{"data":{"unified_token_usage_details":[{"type":"plan","token_usage":1,"token_limit":2},{"type":"plan","token_usage":1,"token_limit":2}]}}`, false, 0, false},
		{"bad counter", `{"data":{"unified_token_usage_details":[{"type":"plan","token_usage":1.5,"token_limit":2}]}}`, false, 0, false},
		{"error", `{"error":{"code":"license_not_found"}}`, false, 0, false},
		{"nothing", `{"data":{"name":"x"}}`, false, 0, false},
	} {
		srv := serveRaw(t, "/api/license/detail/realtime", "k", tc.body)
		got, err := Chatbox{BaseURL: srv.URL, Now: chatboxNow}.Read(context.Background(), "k")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (len(got.Windows) != tc.windows || (tc.windows > 0 && got.Windows[0].ResetsAt.IsZero() == tc.reset))) {
			t.Errorf("%s: %+v %v", tc.name, got, err)
		}
	}
}
