// Package cursor is the HTTP adapter for Cursor's usage endpoints on
// cursor.com, authenticated with the WorkosCursorSessionToken cookie the
// Cursor dashboard uses. It satisfies the UsageSource port of
// internal/contexts/spend/vendorusage/cursor, whose poller turns the
// snapshot into envelopes; that package says which endpoint is read
// when.
package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
	"go.klarlabs.de/tokenops/internal/infra/vendorusage/errbody"
)

// Client reads one account's usage. Cookie + UserID must both be set;
// an empty value short-circuits with usage.ErrMissingCredential so
// callers can distinguish config gaps from network failures.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	Cookie     string
	UserID     string
	// Now is the clock a weekly pool's reset is checked against.
	Now func() time.Time
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

// errRefused is a 401 or 403: the session is not accepted, and no other
// endpoint will accept it either.
var errRefused = errors.New("cursor: the session was refused")

// statusError is a non-2xx answer other than a refusal.
type statusError struct{ msg string }

func (e statusError) Error() string { return e.msg }

// Usage reads GET /api/usage-summary and, best effort, the weekly pool;
// it falls back to the legacy GET /api/usage?user=<UserID> only when
// usage-summary is unavailable (a non-2xx other than a refusal, or no
// answer), and accepts the legacy answer only when it has a request
// allowance. An answer it cannot read is an error naming why.
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
	var s usage.Summary
	err := c.do(ctx, http.MethodGet, "/api/usage-summary", nil, &s)
	switch {
	case err == nil:
		return c.fromSummary(ctx, &s)
	case errors.Is(err, errRefused), errors.Is(err, ctx.Err()) && ctx.Err() != nil:
		return nil, fmt.Errorf("cursor usage-summary: %w", err)
	case isUnavailable(err):
		return c.legacy(ctx, err)
	default:
		return nil, fmt.Errorf("cursor usage-summary: unrecognised answer: %w", err)
	}
}

// isUnavailable is an answer that says nothing about the account: a
// non-2xx status other than a refusal, or no answer at all.
func isUnavailable(err error) bool {
	var se statusError
	var ue *url.Error
	return errors.As(err, &se) || errors.As(err, &ue)
}

func (c *Client) fromSummary(ctx context.Context, s *usage.Summary) (*usage.UsageResponse, error) {
	resp := &usage.UsageResponse{Summary: s}
	// The weekly pool is optional: an account without one, or a failure
	// here, leaves the reading to usage-summary.
	var sand usage.SandStatus
	if c.do(ctx, http.MethodPost, "/api/dashboard/get-sand-usage-status", []byte("{}"), &sand) == nil {
		resp.Pool = sand.Pool(c.now())
	}
	if resp.Pool == nil {
		if _, ok := s.Read(); !ok {
			return nil, errors.New("cursor usage-summary: unrecognised answer: no plan, member or team allowance")
		}
	}
	return resp, nil
}

func (c *Client) legacy(ctx context.Context, summaryErr error) (*usage.UsageResponse, error) {
	q := url.Values{}
	q.Set("user", c.UserID)
	var u usage.UsageResponse
	if err := c.do(ctx, http.MethodGet, "/api/usage?"+q.Encode(), nil, &u); err != nil {
		return nil, fmt.Errorf("cursor usage-summary: %v; legacy usage: %w", summaryErr, err)
	}
	if !u.HasAllowance() {
		return nil, fmt.Errorf("cursor usage-summary: %v; legacy usage: unrecognised answer: no request allowance", summaryErr)
	}
	return &u, nil
}

// do sends one request with the session cookie and decodes a 2xx answer
// into out. A POST carries the site's own Origin, which cursor.com
// requires of state-changing requests.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Cookie", "WorkosCursorSessionToken="+c.Cookie)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		if u, err := url.Parse(c.BaseURL); err == nil {
			req.Header.Set("Origin", u.Scheme+"://"+u.Host)
		}
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s", errRefused, errbody.Status(resp.StatusCode, errbody.Read(resp.Body)))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return statusError{errbody.Status(resp.StatusCode, errbody.Read(resp.Body))}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// NewSource is the poller's client factory: it binds cookie and user ID
// to a Client on cursor.com.
func NewSource(cookie, userID string) usage.UsageSource { return NewClient(cookie, userID) }

// Client satisfies the poller's port.
var _ usage.UsageSource = (*Client)(nil)
