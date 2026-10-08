package accounts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// Readers of a vendor's web console read with the browser session
// `tokenops vendor-usage setup <id>` stored (a Cookie header), the way the
// vendor's own dashboard does. These helpers send such requests.

// browserUA is the User-Agent the console requests carry: the vendors'
// web gateways answer a browser, as CodexBar's readers send it.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"

// isSession reports whether a credential is a Cookie header ("a=b; c=d")
// rather than an API key or a bare token.
func isSession(key string) bool {
	return strings.Contains(key, "=")
}

// cookieValue is the named cookie in a Cookie header, "" when absent. A
// leading "Cookie:" is ignored; names match without regard to case.
func cookieValue(header, name string) string {
	header = cookieHeader(header)
	for part := range strings.SplitSeq(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// errRedirect marks an answer that redirected: a console sends an expired
// session to its sign-in page.
var errRedirect = errors.New("redirected")

// doWeb sends one request carrying a session and returns the body of a
// 200. It never follows a redirect, so the session is never sent to
// another host: a redirect (to a sign-in page), 401 and 403 are
// usage.ErrAuth; any other status is a *statusError. Errors name the
// method, host and path, never the query, a header or the body.
func doWeb(ctx context.Context, hc *http.Client, method, rawURL string, header http.Header, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[http.CanonicalHeaderKey(k)] = v
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", browserUA)
	}
	resp, err := noRedirect(hc).Do(req)
	if err != nil {
		return nil, fmt.Errorf("accounts: %s %s: %w", method, hostPath(rawURL), err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("%w (%w to a sign-in page from %s)", usage.ErrAuth, errRedirect, hostPath(rawURL))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, hostPath(rawURL))
	case resp.StatusCode != http.StatusOK:
		return nil, &statusError{method: method, where: hostPath(rawURL), status: resp.StatusCode}
	}
	return data, nil
}

// noRedirect is hc (or a default client) that returns a redirect instead
// of following it.
func noRedirect(hc *http.Client) *http.Client {
	c := http.Client{Timeout: 20 * time.Second}
	if hc != nil {
		c = *hc
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// formBody encodes fields as a form, in order, every reserved character
// escaped (a security token or a JSON parameter survives intact).
func formBody(fields [][2]string) []byte {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, url.QueryEscape(f[0])+"="+url.QueryEscape(f[1]))
	}
	return []byte(strings.Join(parts, "&"))
}

// looksLikeSignIn reports whether an HTML answer is a sign-in page rather
// than the data asked for.
func looksLikeSignIn(body []byte) bool {
	t := strings.ToLower(string(body[:min(len(body), 64<<10)]))
	return strings.Contains(t, "<html") && (strings.Contains(t, "login") || strings.Contains(t, "sign in") || strings.Contains(t, "signin"))
}
