package copilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot"
)

const sampleResponse = `{
  "login": "test-user",
  "chat_enabled": true,
  "quota_reset_date": "2026-06-01",
  "timestamp_utc": "2026-05-16T07:50:00Z",
  "quota_snapshots": {
    "chat": {"entitlement":300,"remaining":210.5,"percent_remaining":70.0,"overage_count":0,"unlimited":false},
    "premium_interactions": {"entitlement":50,"remaining":50,"percent_remaining":100.0,"unlimited":false}
  }
}`

// Client.User must send the Authorization header in token form, hit
// the right path, and decode the documented response shape.
func TestClientUserHappyPath(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()
	c := NewClient("tok-abc")
	c.BaseURL = srv.URL
	resp, err := c.User(context.Background())
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if gotAuth != "token tok-abc" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotPath != "/copilot_internal/user" {
		t.Errorf("path = %q", gotPath)
	}
	if resp.Login != "test-user" {
		t.Errorf("login = %q", resp.Login)
	}
	chat, ok := resp.QuotaSnapshots["chat"]
	if !ok {
		t.Fatal("chat snapshot missing")
	}
	if chat.PercentRemaining != 70.0 {
		t.Errorf("chat percent_remaining = %v", chat.PercentRemaining)
	}
}

// Empty token short-circuits with ErrNoToken before any HTTP call.
func TestClientUserMissingToken(t *testing.T) {
	c := &Client{}
	_, err := c.User(context.Background())
	if err != usage.ErrNoToken {
		t.Errorf("want ErrNoToken; got %v", err)
	}
}

// Non-2xx response includes status + body snippet so operators can
// diagnose auth failure / rate-limit.
func TestClientUserNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"message":"bad creds"}`))
	}))
	defer srv.Close()
	c := NewClient("bad")
	c.BaseURL = srv.URL
	_, err := c.User(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
}
