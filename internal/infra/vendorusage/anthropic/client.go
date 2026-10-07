// Package anthropic is the HTTP adapter for the Anthropic Admin API
// (https://platform.claude.com/docs/en/api/admin-api): it calls
// GET /v1/organizations/usage_report/messages and satisfies the
// UsageReporter port of internal/contexts/spend/vendorusage/anthropic,
// whose poller turns the report into envelopes.
//
// The package intentionally keeps no global state and accepts every
// dependency through its struct so tests inject a mock transport
// without touching the real network.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
)

// AdminClient is the typed wrapper around the Anthropic Admin API. The
// HTTP transport is injectable so tests run without network access.
type AdminClient struct {
	BaseURL    string
	AdminKey   string
	APIVersion string
	HTTPClient *http.Client
}

// NewAdminClient returns a client with sensible defaults:
//
//   - BaseURL    = https://api.anthropic.com
//   - APIVersion = 2023-06-01 (current as of 2026; rev when the docs change)
//   - HTTPClient is the package default with a 30s timeout
//
// adminKey must be a sk-ant-admin-* key minted via the Claude Console.
// Empty key produces an explicit error on first call rather than a
// silent 401.
func NewAdminClient(adminKey string) *AdminClient {
	return &AdminClient{
		BaseURL:    "https://api.anthropic.com",
		AdminKey:   adminKey,
		APIVersion: "2023-06-01",
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// MessagesUsage calls GET /v1/organizations/usage_report/messages and
// returns the decoded response. Pagination is the caller's
// responsibility: when r.HasMore is true and r.NextPage is non-nil,
// re-call with req.Page set to *r.NextPage.
//
// Failure modes:
//
//   - Empty AdminKey → typed sentinel error before the network call.
//   - Non-2xx response → fmt.Errorf carrying status + body for debug.
//   - JSON parse failure → wrapped error mentioning the endpoint.
func (c *AdminClient) MessagesUsage(ctx context.Context, req usage.MessagesUsageRequest) (*usage.MessagesUsageResponse, error) {
	if c.AdminKey == "" {
		return nil, usage.ErrMissingAdminKey
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.anthropic.com"
	}
	if c.APIVersion == "" {
		c.APIVersion = "2023-06-01"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	q := url.Values{}
	q.Set("starting_at", req.StartingAt.UTC().Format(time.RFC3339))
	if !req.EndingAt.IsZero() {
		q.Set("ending_at", req.EndingAt.UTC().Format(time.RFC3339))
	}
	if req.BucketWidth != "" {
		q.Set("bucket_width", string(req.BucketWidth))
	}
	for _, m := range req.Models {
		q.Add("models[]", m)
	}
	for _, g := range req.GroupBy {
		q.Add("group_by[]", g)
	}
	if req.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", req.Limit))
	}
	if req.Page != "" {
		q.Set("page", req.Page)
	}
	endpoint := c.BaseURL + "/v1/organizations/usage_report/messages?" + q.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("anthropic admin: build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", c.AdminKey)
	httpReq.Header.Set("anthropic-version", c.APIVersion)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic admin: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := readAll(resp.Body, 4096)
		return nil, fmt.Errorf("anthropic admin: usage_report/messages: status %d: %s", resp.StatusCode, body)
	}
	var decoded usage.MessagesUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("anthropic admin: decode response: %w", err)
	}
	return &decoded, nil
}

// AdminClient satisfies the poller's port.
var _ usage.UsageReporter = (*AdminClient)(nil)
