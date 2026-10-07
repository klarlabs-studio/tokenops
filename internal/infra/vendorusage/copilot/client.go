// Package copilot is the HTTP adapter for GitHub Copilot's internal user
// endpoint (`api.github.com/copilot_internal/user`). It satisfies the
// UserSource port of internal/contexts/spend/vendorusage/copilot, whose
// poller turns the quota snapshots into envelopes.
package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot"
)

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
func (c *Client) User(ctx context.Context) (*usage.UserResponse, error) {
	if c.OAuthToken == "" {
		return nil, usage.ErrNoToken
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
	var u usage.UserResponse
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("copilot user: decode: %w", err)
	}
	return &u, nil
}

// NewSource is the poller's client factory: it binds token to a Client
// on GitHub's API.
func NewSource(token string) usage.UserSource { return NewClient(token) }

// Client satisfies the poller's port.
var _ usage.UserSource = (*Client)(nil)
