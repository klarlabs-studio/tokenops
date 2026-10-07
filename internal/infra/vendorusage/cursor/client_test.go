package cursor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
)

const sampleResponse = `{
  "gpt-4": {"numRequests": 120, "maxRequestUsage": 500},
  "gpt-4-32k": {"numRequests": 12, "maxRequestUsage": 50},
  "premiumRequests": {"numRequests": 0, "maxRequestUsage": 0},
  "startOfMonth": "2026-05-01T00:00:00.000Z"
}`

// Client.Usage sends WorkosCursorSessionToken in the Cookie header,
// uses ?user= query, and decodes the per-model map.
func TestClientUsageHappyPath(t *testing.T) {
	var gotCookie, gotUser, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotUser = r.URL.Query().Get("user")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleResponse))
	}))
	defer srv.Close()

	c := NewClient("tok-xyz", "user-123")
	c.BaseURL = srv.URL
	resp, err := c.Usage(context.Background())
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if gotCookie != "WorkosCursorSessionToken=tok-xyz" {
		t.Errorf("cookie header = %q", gotCookie)
	}
	if gotUser != "user-123" {
		t.Errorf("user query = %q", gotUser)
	}
	if gotPath != "/api/usage" {
		t.Errorf("path = %q", gotPath)
	}
	if len(resp.Models) != 3 {
		t.Fatalf("want 3 model rows; got %d", len(resp.Models))
	}
	if resp.Models["gpt-4"].NumRequests != 120 {
		t.Errorf("gpt-4 numRequests = %d", resp.Models["gpt-4"].NumRequests)
	}
	if resp.StartOfMonth != "2026-05-01T00:00:00.000Z" {
		t.Errorf("startOfMonth = %q", resp.StartOfMonth)
	}
}

// Either Cookie or UserID empty → ErrMissingCredential, no HTTP call.
func TestClientUsageMissingCredentials(t *testing.T) {
	if _, err := (&Client{Cookie: "x"}).Usage(context.Background()); err != usage.ErrMissingCredential {
		t.Errorf("want ErrMissingCredential when UserID empty; got %v", err)
	}
	if _, err := (&Client{UserID: "u"}).Usage(context.Background()); err != usage.ErrMissingCredential {
		t.Errorf("want ErrMissingCredential when Cookie empty; got %v", err)
	}
}

// Non-2xx surfaces status + body snippet.
func TestClientUsageNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"expired cookie"}`))
	}))
	defer srv.Close()
	c := NewClient("bad", "u")
	c.BaseURL = srv.URL
	if _, err := c.Usage(context.Background()); err == nil {
		t.Fatal("want error")
	}
}
