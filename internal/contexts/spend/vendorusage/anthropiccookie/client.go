// Package anthropiccookie scrapes claude.ai's session-authenticated
// usage endpoint. This is the silver-bullet signal for Claude Max
// subscribers — same data Anthropic's own UI shows: 5-hour, weekly
// all-models, and weekly Opus utilization percentages plus the reset
// timestamps. The cookie-scraping approach is undocumented and
// ToS-grey, but it is what every credible Claude usage tracker
// (Claude-Usage-Tracker, claude-bar variants) does, because no
// documented endpoint exposes Max-plan window state.
//
// Two-step flow:
//
//  1. GET claude.ai/api/organizations
//     → returns a JSON array of orgs the operator belongs to;
//     pick the first with `capabilities` including a Max/Pro entry.
//  2. GET claude.ai/api/organizations/{org_id}/usage
//     → returns the actual percentages.
//
// Operator UX: paste the `sessionKey` cookie from a browser devtools
// inspect (Application → Cookies → claude.ai). The cookie rolls every
// few weeks; the daemon logs WARN when it expires so the operator
// knows to re-paste.
package anthropiccookie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OrgEntry is one row from /api/organizations.
type OrgEntry struct {
	UUID         string   `json:"uuid"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// UsageResponse mirrors the /api/organizations/{org_id}/usage payload.
// All three Window blocks share the same shape, but we keep them as
// concrete fields rather than a map so the field names appear in
// downstream telemetry without us guessing keys.
type UsageResponse struct {
	FiveHour     Window      `json:"five_hour"`
	SevenDay     Window      `json:"seven_day"`
	SevenDayOpus Window      `json:"seven_day_opus"`
	ExtraUsage   *ExtraUsage `json:"extra_usage,omitempty"`
}

// Window is one rate-limit window's snapshot.
type Window struct {
	UtilizationPct float64 `json:"utilization_pct"`
	ResetAt        string  `json:"reset_at,omitempty"`
}

// ExtraUsage captures the operator's add-on (overage) credit balance
// when their plan allows top-ups beyond the standard cap.
type ExtraUsage struct {
	CurrentSpending float64 `json:"current_spending"`
	BudgetLimit     float64 `json:"budget_limit"`
}

// Client wraps the claude.ai cookie-authenticated endpoints.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	SessionKey string
}

// ErrMissingCookie signals an empty session_key in config; poller
// stays idle and the CLI hint surfaces the fix.
var ErrMissingCookie = errors.New("anthropic-cookie: session_key required (paste from claude.ai devtools → Application → Cookies → sessionKey)")

// ErrUnauthorized indicates the cookie has expired or is invalid.
// Surfaced separately so the daemon can log a distinct WARN telling
// the operator to re-paste rather than burying the 401 in generic
// http error noise.
var ErrUnauthorized = errors.New("anthropic-cookie: claude.ai returned 401 — sessionKey likely expired, re-paste from devtools")

// NewClient binds a session cookie and returns a Client with sensible
// defaults. The UA mimics a recent Chrome to avoid Cloudflare bot
// challenges; without it the request 403s.
func NewClient(sessionKey string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://claude.ai",
		SessionKey: sessionKey,
	}
}

// Organizations fetches the list of orgs the cookie belongs to.
// Used to discover an org_id when the operator hasn't pinned one in
// config; with a single-org account this is a one-line resolution.
func (c *Client) Organizations(ctx context.Context) ([]OrgEntry, error) {
	if c.SessionKey == "" {
		return nil, ErrMissingCookie
	}
	body, err := c.get(ctx, "/api/organizations")
	if err != nil {
		return nil, err
	}
	var orgs []OrgEntry
	if err := json.Unmarshal(body, &orgs); err != nil {
		return nil, fmt.Errorf("anthropic-cookie: decode organizations: %w", err)
	}
	return orgs, nil
}

// Usage fetches the per-org usage snapshot.
func (c *Client) Usage(ctx context.Context, orgID string) (*UsageResponse, error) {
	if c.SessionKey == "" {
		return nil, ErrMissingCookie
	}
	if orgID == "" {
		return nil, fmt.Errorf("anthropic-cookie: org_id is required")
	}
	body, err := c.get(ctx, "/api/organizations/"+orgID+"/usage")
	if err != nil {
		return nil, err
	}
	var u UsageResponse
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("anthropic-cookie: decode usage: %w", err)
	}
	return &u, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	if c.BaseURL == "" {
		c.BaseURL = "https://claude.ai"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("anthropic-cookie: build request: %w", err)
	}
	req.Header.Set("Cookie", "sessionKey="+c.SessionKey)
	req.Header.Set("Accept", "application/json")
	// Cloudflare in front of claude.ai 403s anything that doesn't look
	// like a browser. A vanilla Chrome UA is sufficient and tolerated
	// — we're identifying as the same caller every browser tab is.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic-cookie: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("anthropic-cookie: %s: status %d: %s", path, resp.StatusCode, snippet)
	}
	return io.ReadAll(resp.Body)
}
