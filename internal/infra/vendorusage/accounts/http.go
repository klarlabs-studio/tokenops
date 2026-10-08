// Package accounts holds the HTTP readers for gateway and API vendors' own
// account figures (ADR 0009 §7): OpenRouter, DeepSeek, Moonshot, the
// subscription plans in plans.go and the gateways in gateways.go. Each
// satisfies a port of internal/contexts/spend/vendorusage/accounts, whose
// poller decides which key goes where. Each reader calls only its vendor's
// documented endpoint; keys are never stored or logged.
package accounts

//go:generate go run go.klarlabs.de/tokenops/internal/tools/gen/listgen -out readers_gen.go -list readers=reader:usage.Reader -list gateways=gateway:usage.Gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// The readers were written against each vendor's published endpoint, or
// the vendor's own client source where the endpoint is not published; each
// says which, and its provider descriptor records how far it has been
// verified (internal/contexts/spend/providers).

// Readers is every vendor with an account endpoint we read.
func Readers() []usage.Reader {
	out := make([]usage.Reader, 0, len(readers))
	for _, f := range readers {
		out = append(out, f())
	}
	return out
}

// Gateways is every gateway whose budget the calling key can read.
// Portkey and Cloudflare AI Gateway are not here: neither lets the key in
// use read its own spend.
func Gateways() []usage.Gateway {
	out := make([]usage.Gateway, 0, len(gateways))
	for _, f := range gateways {
		out = append(out, f())
	}
	return out
}

// getJSON GETs url with a bearer key and decodes the body into out.
func getJSON(ctx context.Context, hc *http.Client, url, key string, out any) error {
	return getJSONAuth(ctx, hc, url, "Bearer "+key, out)
}

// getJSONAuth GETs url with the Authorization header set to auth.
func getJSONAuth(ctx context.Context, hc *http.Client, url, auth string, out any) error {
	return getJSONHeader(ctx, hc, url, "Authorization", auth, out)
}

// getJSONHeader GETs url with the key in header (ElevenLabs' xi-api-key).
func getJSONHeader(ctx context.Context, hc *http.Client, url, header, value string, out any) error {
	return sendJSON(ctx, hc, http.MethodGet, url, nil, func(h http.Header) { h.Set(header, value) }, out)
}

// statusError is an answer that is neither 200 nor a refusal. A reader
// whose vendor gives a status a meaning (ai&'s 402, out of credit) reads
// it with errors.As.
type statusError struct {
	method, where string
	status        int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("accounts: %s %s: status %d", e.method, e.where, e.status)
}

// sendJSON sends a request with body (nil for none), the headers auth
// sets, and decodes a 200's body into out. 401 and 403 are usage.ErrAuth;
// errors name the host and path, never the query string.
func sendJSON(ctx context.Context, hc *http.Client, method, url string, body []byte, auth func(http.Header), out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	auth(req.Header)
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("accounts: %s %s: %w", method, hostPath(url), err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, hostPath(url))
	case resp.StatusCode != http.StatusOK:
		return &statusError{method: method, where: hostPath(url), status: resp.StatusCode}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("accounts: %s %s: %w", method, hostPath(url), err)
	}
	return nil
}

func hostPath(url string) string {
	if i := strings.Index(url, "?"); i >= 0 {
		return url[:i]
	}
	return url
}

func base(override, def string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return def
}

// number decodes a JSON number or a numeric string.
type number struct {
	v  float64
	ok bool
}

func (n *number) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return nil // an unreadable figure is absent, not an error
	}
	n.v, n.ok = v, true
	return nil
}

// windowName names a window by its length: "5h", "week", "month".
func windowName(d time.Duration) string {
	switch {
	case d >= 28*24*time.Hour && d <= 31*24*time.Hour:
		return "month"
	case d == 7*24*time.Hour:
		return "week"
	case d == 24*time.Hour:
		return "day"
	case d > 0 && d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d > 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return "window"
}

// parseTime reads RFC 3339, or an ISO time without a zone as UTC.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func pct(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return used / limit * 100
}

// probe GETs url with no key and returns the body of a 200, for
// recognising a gateway by its health route.
func probe(ctx context.Context, hc *http.Client, url string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return body, err == nil
}

// getGateway GETs a gateway route with the key in header.
func getGateway(ctx context.Context, hc *http.Client, url, header, value string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set(header, value)
	req.Header.Set("Accept", "application/json")
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("accounts: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, url)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("accounts: GET %s: status %d", url, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("accounts: GET %s: %w", url, err)
	}
	return nil
}
