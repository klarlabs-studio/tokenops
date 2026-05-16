// Package cursor polls Cursor's per-user usage endpoint
// (cursor.com/api/usage?user=<id>) using the WorkosCursorSessionToken
// cookie the Cursor IDE itself uses. Same data the IDE's status-bar
// usage indicator reads. The endpoint is internal — Cursor doesn't
// publish a contract — but it's stable enough that every third-party
// Cursor usage tracker (cursor-stats, cursor-usage-tracker,
// cursor_api_demo) relies on it.
//
// Auth: cookie. Operators paste WorkosCursorSessionToken + user_id
// into config (or env), or — future enhancement — TokenOps reads
// them from the local Cursor IDE state.vscdb SQLite store. For now
// the explicit-config path is the only one wired.
//
// Honesty caveat: this is ToS-grey territory. Cursor's own status-bar
// extension uses the endpoint so it's tolerated, but the contract
// can shift without notice. signal_quality marks any observation
// HIGH while telling consumers the source is undocumented.
package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// UsageResponse mirrors the observed cursor.com /api/usage payload.
// The map keys are model identifiers (`gpt-4`, `gpt-4-32k`,
// `premiumRequests`, …); each value carries the per-window request
// count + the entitlement cap.
type UsageResponse struct {
	Models       map[string]ModelUsage `json:"-"`
	StartOfMonth string                `json:"startOfMonth,omitempty"`
}

// ModelUsage is one row of the response. NumRequests is the in-window
// count; MaxRequestUsage is the plan's cap (0 = unlimited).
type ModelUsage struct {
	NumRequests     int `json:"numRequests"`
	MaxRequestUsage int `json:"maxRequestUsage,omitempty"`
}

// UnmarshalJSON treats the response as a flat map keyed by model
// name. `startOfMonth` is hoisted into its own field; everything
// else lands in Models.
func (u *UsageResponse) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.Models = make(map[string]ModelUsage, len(raw))
	for k, v := range raw {
		if k == "startOfMonth" {
			if err := json.Unmarshal(v, &u.StartOfMonth); err == nil {
				continue
			}
		}
		var m ModelUsage
		if err := json.Unmarshal(v, &m); err == nil {
			u.Models[k] = m
		}
	}
	return nil
}

// Client wraps the Cursor /api/usage endpoint. Cookie + UserID must
// both be set; an empty value short-circuits with ErrMissingCredential
// so callers can distinguish config gaps from network failures.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	Cookie     string
	UserID     string
}

// ErrMissingCredential signals that either Cookie or UserID is
// unconfigured. Poller treats this as "stay idle" rather than fatal.
var ErrMissingCredential = errors.New("cursor: WorkosCursorSessionToken + user_id required")

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
func (c *Client) Usage(ctx context.Context) (*UsageResponse, error) {
	if c.Cookie == "" || c.UserID == "" {
		return nil, ErrMissingCredential
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
	var u UsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("cursor usage: decode: %w", err)
	}
	return &u, nil
}
