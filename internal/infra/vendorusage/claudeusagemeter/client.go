// Package claudeusagemeter is the HTTP adapter for claude.ai's
// session-authenticated organizations and usage endpoints. It sends the
// browser's sessionKey and Cloudflare clearance cookies and satisfies the
// SessionClient port of internal/contexts/spend/vendorusage/claudeusagemeter,
// whose poller turns the snapshot into envelopes.
package claudeusagemeter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
)

// Client wraps the claude.ai cookie-authenticated endpoints.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	SessionKey string
	// Clearance is Cloudflare's cf_clearance cookie from the browser the
	// session came from. claude.ai sits behind a bot check that answers
	// 403 "Just a moment..." to a client it does not recognise; that
	// cookie is the browser's proof of having passed it, and it is bound
	// to the browser's User-Agent, so the two travel together.
	Clearance string
	// UserAgent is that browser's own agent string. Empty falls back to a
	// plain Chrome string, which passes on its own only when the bot
	// check is not asking.
	UserAgent string
	// BrowserHeaders contains only the non-secret client-hint and fetch
	// metadata allowlisted by the copied-request importer.
	BrowserHeaders map[string]string
	// BrowserCookies contains only Cloudflare bot-management cookie names.
	// The request path enforces the allowlist again so hand-edited config
	// cannot turn this into arbitrary cookie replay.
	BrowserCookies map[string]string
}

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
func (c *Client) Organizations(ctx context.Context) ([]usage.OrgEntry, error) {
	if c.SessionKey == "" {
		return nil, usage.ErrMissingCookie
	}
	body, err := c.get(ctx, "/api/organizations")
	if err != nil {
		return nil, err
	}
	var orgs []usage.OrgEntry
	if err := json.Unmarshal(body, &orgs); err != nil {
		return nil, fmt.Errorf("claude-usage-meter: decode organizations: %w", err)
	}
	return orgs, nil
}

// Usage fetches the per-org usage snapshot.
func (c *Client) Usage(ctx context.Context, orgID string) (*usage.UsageResponse, error) {
	if c.SessionKey == "" {
		return nil, usage.ErrMissingCookie
	}
	if orgID == "" {
		return nil, fmt.Errorf("claude-usage-meter: org_id is required")
	}
	body, err := c.get(ctx, "/api/organizations/"+orgID+"/usage")
	if err != nil {
		return nil, err
	}
	return usage.ParseUsage(body)
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
		return nil, fmt.Errorf("claude-usage-meter: build request: %w", err)
	}
	req.AddCookie(&http.Cookie{Name: "sessionKey", Value: c.SessionKey})
	if c.Clearance != "" {
		req.AddCookie(&http.Cookie{Name: "cf_clearance", Value: c.Clearance})
	}
	for name, value := range c.BrowserCookies {
		if allowedBrowserCookie(name) && value != "" {
			req.AddCookie(&http.Cookie{Name: name, Value: value})
		}
	}
	req.Header.Set("Accept", "application/json")
	// Cloudflare in front of claude.ai 403s anything that doesn't look
	// like a browser. A vanilla Chrome UA is sufficient and tolerated
	// — we're identifying as the same caller every browser tab is.
	ua := c.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}
	req.Header.Set("User-Agent", ua)
	for name, value := range c.BrowserHeaders {
		if allowedBrowserHeader(name) && value != "" && len(value) <= 1024 && !strings.ContainsAny(value, "\r\n") {
			req.Header.Set(name, value)
		}
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude-usage-meter: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, usage.ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusForbidden && isBotCheck(snippet) {
			return nil, fmt.Errorf("%w (%s)", usage.ErrBotCheck, path)
		}
		if resp.StatusCode == http.StatusForbidden && isExpiredSession(snippet) {
			return nil, usage.ErrUnauthorized
		}
		return nil, fmt.Errorf("claude-usage-meter: %s: status %d: %s", path, resp.StatusCode, snippet)
	}
	return io.ReadAll(resp.Body)
}

func allowedBrowserHeader(name string) bool {
	switch strings.ToLower(name) {
	case "accept-language", "priority", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
		"sec-fetch-dest", "sec-fetch-mode", "sec-fetch-site":
		return true
	default:
		return false
	}
}

func allowedBrowserCookie(name string) bool {
	switch name {
	case "__cf_bm", "_cfuvid":
		return true
	default:
		return false
	}
}

// isExpiredSession recognises claude.ai's answer to an expired session: a
// 403 permission_error naming account_session_invalid rather than a 401.
func isExpiredSession(body []byte) bool {
	return strings.Contains(string(body), `"account_session_invalid"`)
}

// isBotCheck recognises Cloudflare's interstitial, which arrives as a 403
// carrying an HTML challenge page rather than an API error.
func isBotCheck(body []byte) bool {
	b := strings.ToLower(string(body))
	return strings.Contains(b, "just a moment") || strings.Contains(b, "cf-browser-verification") ||
		strings.Contains(b, "cloudflare")
}

// Session implements usage.SessionClient.
func (c *Client) Session() usage.Session {
	return usage.Session{Key: c.SessionKey, Clearance: c.Clearance, UserAgent: c.UserAgent}
}

// SetSession implements usage.SessionClient.
func (c *Client) SetSession(s usage.Session) {
	c.SessionKey, c.Clearance, c.UserAgent = s.Key, s.Clearance, s.UserAgent
}

// NewSessionClient is the poller's client factory: a Client on claude.ai
// carrying cfg's session, clearance, agent and allowlisted browser headers
// and cookies.
func NewSessionClient(cfg usage.ClientConfig) usage.SessionClient {
	c := NewClient(cfg.SessionKey)
	c.Clearance, c.UserAgent = cfg.Clearance, cfg.UserAgent
	c.BrowserHeaders = cfg.BrowserHeaders
	c.BrowserCookies = cfg.BrowserCookies
	return c
}

// Client satisfies the poller's port.
var _ usage.SessionClient = (*Client)(nil)
