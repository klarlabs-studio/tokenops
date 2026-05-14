// Package anthropic polls the Anthropic Admin API
// (https://platform.claude.com/docs/en/api/admin-api) for usage
// telemetry and emits PromptEvent envelopes with Source="vendor-usage-anthropic".
// The signal it produces is the highest-confidence Anthropic input
// TokenOps can offer today: per-bucket token counts attributed to
// actual API key + workspace.
//
// Scope today:
//
//   - GET /v1/organizations/usage_report/messages — bucketed token
//     counts. Requires an Admin API key (sk-ant-admin-...). Per
//     research, this endpoint covers metered API usage only; Claude
//     Max plan window state is NOT exposed and remains heuristic.
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

// BucketWidth captures the API's bucket_width enum. The Admin API
// caps how many buckets a single response carries (1m → 1440, 1h →
// 168, 1d → 31) so the poller's default 1h gives 7 days of history
// per request without pagination.
type BucketWidth string

const (
	BucketWidthMinute BucketWidth = "1m"
	BucketWidthHour   BucketWidth = "1h"
	BucketWidthDay    BucketWidth = "1d"
)

// MessagesUsageRequest mirrors the documented query parameters for
// GET /v1/organizations/usage_report/messages. We expose only the
// fields the poller currently uses; expanding the surface is cheap.
type MessagesUsageRequest struct {
	StartingAt  time.Time
	EndingAt    time.Time
	BucketWidth BucketWidth
	Models      []string
	GroupBy     []string
	Limit       int
	Page        string
}

// MessagesUsageResponse decodes the API response shape verbatim. The
// nested Cache and ServerToolUse structs handle the typed details
// from the docs; we don't try to flatten them so a future field
// addition lands cleanly without breaking the parse.
type MessagesUsageResponse struct {
	Data     []UsageBucket `json:"data"`
	HasMore  bool          `json:"has_more"`
	NextPage *string       `json:"next_page,omitempty"`
}

// UsageBucket is one row from the response. StartingAt/EndingAt mark
// the bucket window; Results is the per-model (or per-group) entries
// inside.
type UsageBucket struct {
	StartingAt time.Time     `json:"starting_at"`
	EndingAt   time.Time     `json:"ending_at"`
	Results    []UsageResult `json:"results"`
}

// UsageResult is one model/workspace/api_key combination's tokens
// within a bucket. The cache_creation struct distinguishes 5-minute
// vs 1-hour ephemeral caches; we keep both so cost recompute can do
// the right thing once we wire Anthropic's cache pricing.
type UsageResult struct {
	UncachedInputTokens  int64           `json:"uncached_input_tokens"`
	CacheReadInputTokens int64           `json:"cache_read_input_tokens"`
	CacheCreation        CacheCreation   `json:"cache_creation"`
	OutputTokens         int64           `json:"output_tokens"`
	ServerToolUse        ServerToolUse   `json:"server_tool_use"`
	Model                string          `json:"model"`
	WorkspaceID          *string         `json:"workspace_id,omitempty"`
	APIKeyID             *string         `json:"api_key_id,omitempty"`
	ServiceTier          string          `json:"service_tier,omitempty"`
	ContextWindow        string          `json:"context_window,omitempty"`
	InferenceGeo         string          `json:"inference_geo,omitempty"`
	Raw                  json.RawMessage `json:"-"`
}

type CacheCreation struct {
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
}

type ServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests"`
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
func (c *AdminClient) MessagesUsage(ctx context.Context, req MessagesUsageRequest) (*MessagesUsageResponse, error) {
	if c.AdminKey == "" {
		return nil, ErrMissingAdminKey
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
	var decoded MessagesUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("anthropic admin: decode response: %w", err)
	}
	return &decoded, nil
}

// ErrMissingAdminKey is returned by MessagesUsage when no admin key
// is configured. The poller checks this error to log a clear
// "configure vendor_usage.anthropic.admin_key" hint rather than a
// generic auth failure.
var ErrMissingAdminKey = fmt.Errorf("anthropic admin: admin key not configured")
