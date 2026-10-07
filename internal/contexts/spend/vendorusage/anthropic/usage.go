// Package anthropic turns the Anthropic Admin API's usage report
// (https://platform.claude.com/docs/en/api/admin-api) into PromptEvent
// envelopes with Source="vendor-usage-anthropic". The signal it produces
// is the highest-confidence Anthropic input TokenOps can offer today:
// per-bucket token counts attributed to actual API key + workspace.
//
// Scope today:
//
//   - GET /v1/organizations/usage_report/messages — bucketed token
//     counts. Requires an Admin API key (sk-ant-admin-...). Per
//     research, this endpoint covers metered API usage only; Claude
//     Max plan window state is NOT exposed and remains heuristic.
//
// The package holds the report's value types, the poller and the mapping
// to envelopes. The HTTP client lives in internal/infra/vendorusage/anthropic
// and reaches the poller through the UsageReporter port.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// UsageReporter reads the Admin API's messages usage report. The HTTP
// client in internal/infra/vendorusage/anthropic satisfies it.
type UsageReporter interface {
	MessagesUsage(ctx context.Context, req MessagesUsageRequest) (*MessagesUsageResponse, error)
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

// ErrMissingAdminKey is returned by MessagesUsage when no admin key
// is configured. The poller checks this error to log a clear
// "configure vendor_usage.anthropic.admin_key" hint rather than a
// generic auth failure.
var ErrMissingAdminKey = fmt.Errorf("anthropic admin: admin key not configured")
