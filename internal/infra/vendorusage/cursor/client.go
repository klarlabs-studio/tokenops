// Package cursor is the HTTP adapter for Cursor's per-user usage endpoint
// (cursor.com/api/usage?user=<id>), authenticated with the
// WorkosCursorSessionToken cookie the Cursor IDE itself uses. It
// satisfies the UsageSource port of
// internal/contexts/spend/vendorusage/cursor, whose poller turns the
// snapshot into envelopes.
package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
)

// Client wraps the Cursor /api/usage endpoint. Cookie + UserID must
// both be set; an empty value short-circuits with usage.ErrMissingCredential
// so callers can distinguish config gaps from network failures.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	Cookie     string
	UserID     string
}

// NewClient binds cookie + user ID and returns a Client with sensible
// defaults.
func NewClient(cookie, userID string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://cursor.com",
		Cookie:     cookie,
		UserID:     userID,
	}
}

// Usage hits GET /api/usage?user=<UserID> with WorkosCursorSessionToken
// in the Cookie header. Returns typed errors for empty creds /
// non-2xx / decode failure.
func (c *Client) Usage(ctx context.Context) (*usage.UsageResponse, error) {
	if c.Cookie == "" || c.UserID == "" {
		return nil, usage.ErrMissingCredential
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://cursor.com"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	q := url.Values{}
	q.Set("user", c.UserID)
	endpoint := c.BaseURL + "/api/usage?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("cursor usage: build request: %w", err)
	}
	req.Header.Set("Cookie", "WorkosCursorSessionToken="+c.Cookie)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cursor usage: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("cursor usage: status %d: %s", resp.StatusCode, snippet)
	}
	var u usage.UsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("cursor usage: decode: %w", err)
	}
	return &u, nil
}

// NewSource is the poller's client factory: it binds cookie and user ID
// to a Client on cursor.com.
func NewSource(cookie, userID string) usage.UsageSource { return NewClient(cookie, userID) }

// Client satisfies the poller's port.
var _ usage.UsageSource = (*Client)(nil)
