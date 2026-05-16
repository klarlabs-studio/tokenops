package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UserResponse mirrors the (undocumented but stable since 2022)
// `GET api.github.com/copilot_internal/user` shape. We capture the
// fields the IDE plugins read: quota snapshots for chat and
// premium-interaction buckets, plus a freshness timestamp.
type UserResponse struct {
	Login          string                   `json:"login"`
	ChatEnabled    bool                     `json:"chat_enabled"`
	QuotaResetDate string                   `json:"quota_reset_date"`
	QuotaSnapshots map[string]QuotaSnapshot `json:"quota_snapshots"`
	TimestampUTC   string                   `json:"timestamp_utc"`
}

// QuotaSnapshot is one row of the quota_snapshots map. Keys observed
// in the wild: `chat`, `premium_interactions`, `completions`.
// `Unlimited=true` means the operator is on a plan with no cap
// (typically Copilot Business / Enterprise); UI shows ∞.
type QuotaSnapshot struct {
	Entitlement      int     `json:"entitlement"`
	Remaining        float64 `json:"remaining"`
	PercentRemaining float64 `json:"percent_remaining"`
	OverageCount     int     `json:"overage_count"`
	Unlimited        bool    `json:"unlimited"`
}

// Client wraps the Copilot internal-user endpoint. HTTPClient is
// injectable so tests stub the transport without touching the
// network. BaseURL defaults to https://api.github.com.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	OAuthToken string
}

// NewClient binds an OAuth token and returns a Client with sensible
// defaults (api.github.com, 30s timeout).
func NewClient(token string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://api.github.com",
		OAuthToken: token,
	}
}

// User fetches the operator's current Copilot user record. Returns a
// typed error for empty token / non-2xx / decode failure so the
// poller can log the right thing.
func (c *Client) User(ctx context.Context) (*UserResponse, error) {
	if c.OAuthToken == "" {
		return nil, ErrNoToken
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.github.com"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/copilot_internal/user", nil)
	if err != nil {
		return nil, fmt.Errorf("copilot user: build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.OAuthToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot user: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("copilot user: status %d: %s", resp.StatusCode, snippet)
	}
	var u UserResponse
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("copilot user: decode: %w", err)
	}
	return &u, nil
}
