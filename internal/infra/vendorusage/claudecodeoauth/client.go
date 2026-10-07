// Package claudecodeoauth holds the adapters for reading Claude's plan
// windows with Claude Code's own sign-in (ADR 0011, source class 4): the
// HTTP client for Anthropic's OAuth usage endpoint, and the two places
// Claude Code keeps its sign-in — its credentials file and, on macOS, the
// Keychain item read through /usr/bin/security. They satisfy the ports of
// internal/contexts/spend/vendorusage/claudecodeoauth.
//
// The token is held in memory only, sent only to api.anthropic.com, and
// never refreshed here: a refresh rotates the token and would sign Claude
// Code out.
package claudecodeoauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	oauth "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
)

// DefaultBaseURL is Anthropic's API, the only host the token is sent to.
const DefaultBaseURL = "https://api.anthropic.com"

const (
	usagePath = "/api/oauth/usage"
	// betaHeader is required by the OAuth usage endpoint.
	betaHeader = "oauth-2025-04-20"
)

// rateLimitWait is how long to wait after a 429 that names no time.
const rateLimitWait = 10 * time.Minute

// Client calls the OAuth usage endpoint.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	// UserAgent names TokenOps honestly; the endpoint is not told it is
	// Claude Code.
	UserAgent string
}

// Usage reads the plan's windows with token.
func (c Client) Usage(ctx context.Context, token string, now time.Time) (*claudeusagemeter.UsageResponse, error) {
	base, hc := c.BaseURL, c.HTTP
	if base == "" {
		base = DefaultBaseURL
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+usagePath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", betaHeader)
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude-code-oauth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return claudeusagemeter.ParseUsage(body)
	case http.StatusUnauthorized:
		return nil, oauth.ErrExpired
	case http.StatusTooManyRequests:
		return nil, &oauth.RateLimitedError{Until: retryAfter(resp.Header.Get("Retry-After"), now)}
	}
	// The body is an API error, never a credential; a short one is kept
	// so the reason reaches the log.
	return nil, fmt.Errorf("claude-code-oauth: usage: status %d: %.200s", resp.StatusCode, body)
}

func retryAfter(v string, now time.Time) time.Time {
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return now.Add(time.Duration(s) * time.Second)
	}
	if t, err := http.ParseTime(v); err == nil {
		return t
	}
	return now.Add(rateLimitWait)
}

// Client satisfies the poller's port.
var _ oauth.UsageClient = Client{}
