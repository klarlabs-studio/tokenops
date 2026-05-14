package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// MessagesUsage sends the documented headers + query params and
// decodes a well-formed response.
func TestMessagesUsageHappyPath(t *testing.T) {
	want := `{
		"data": [{
			"starting_at": "2026-05-14T00:00:00Z",
			"ending_at":   "2026-05-14T01:00:00Z",
			"results": [{
				"uncached_input_tokens": 1000,
				"cache_read_input_tokens": 500,
				"cache_creation": {"ephemeral_5m_input_tokens": 50, "ephemeral_1h_input_tokens": 0},
				"output_tokens": 200,
				"server_tool_use": {"web_search_requests": 1},
				"model": "claude-opus-4-7",
				"service_tier": "standard",
				"context_window": "0-200k",
				"inference_geo": "us"
			}]
		}],
		"has_more": false,
		"next_page": null
	}`
	var (
		gotKey     string
		gotVersion string
		gotPath    string
		gotQuery   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(want))
	}))
	defer srv.Close()

	c := NewAdminClient("sk-ant-admin-test")
	c.BaseURL = srv.URL
	resp, err := c.MessagesUsage(context.Background(), MessagesUsageRequest{
		StartingAt:  time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		EndingAt:    time.Date(2026, 5, 14, 1, 0, 0, 0, time.UTC),
		BucketWidth: BucketWidthHour,
		GroupBy:     []string{"model"},
	})
	if err != nil {
		t.Fatalf("MessagesUsage: %v", err)
	}
	if gotKey != "sk-ant-admin-test" {
		t.Errorf("missing x-api-key header; got %q", gotKey)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("anthropic-version = %q", gotVersion)
	}
	if gotPath != "/v1/organizations/usage_report/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "bucket_width=1h") {
		t.Errorf("query missing bucket_width=1h: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "group_by%5B%5D=model") {
		t.Errorf("query missing group_by[]=model: %q", gotQuery)
	}
	if len(resp.Data) != 1 || len(resp.Data[0].Results) != 1 {
		t.Fatalf("unexpected response shape: %+v", resp)
	}
	if got := resp.Data[0].Results[0].Model; got != "claude-opus-4-7" {
		t.Errorf("model = %q", got)
	}
}

// Empty admin key returns ErrMissingAdminKey before any HTTP call.
func TestMessagesUsageMissingKey(t *testing.T) {
	c := &AdminClient{}
	_, err := c.MessagesUsage(context.Background(), MessagesUsageRequest{
		StartingAt: time.Now(),
	})
	if !errors.Is(err, ErrMissingAdminKey) {
		t.Fatalf("want ErrMissingAdminKey; got %v", err)
	}
}

// Non-2xx response includes status + body snippet in the error so
// operators can diagnose 401 / 429 etc.
func TestMessagesUsageNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"invalid admin key"}`))
	}))
	defer srv.Close()
	c := NewAdminClient("bad-key")
	c.BaseURL = srv.URL
	_, err := c.MessagesUsage(context.Background(), MessagesUsageRequest{
		StartingAt: time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401 error; got %v", err)
	}
}
