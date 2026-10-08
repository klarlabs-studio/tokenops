package accounts

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// Helpers for the readers that sign in with a browser session: the
// credential is the Cookie header `tokenops vendor-usage setup <id>` read
// from the browser or was pasted, and it is sent only to the vendor's own
// web host.

// browserUA is the agent string a vendor's web app sees from Chrome on
// macOS; some dashboards refuse a request without one.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"

// cookieValue is the value of cookie name in a Cookie header, "" when the
// header has none. A leading "Cookie:" is ignored; names match without
// regard to case.
func cookieValue(header, name string) string {
	header = strings.TrimSpace(header)
	if len(header) > 7 && strings.EqualFold(header[:7], "cookie:") {
		header = header[7:]
	}
	for part := range strings.SplitSeq(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// sessionToken is a session cookie's value from a Cookie header holding
// it, or the credential itself when it is the bare value (no "=" or ";").
func sessionToken(credential, name string) string {
	credential = strings.TrimSpace(credential)
	if credential != "" && !strings.ContainsAny(credential, "=;") {
		return credential
	}
	return cookieValue(credential, name)
}

// cookieHeader is a pasted Cookie header without a leading "Cookie:".
func cookieHeader(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 7 && strings.EqualFold(raw[:7], "cookie:") {
		raw = strings.TrimSpace(raw[7:])
	}
	return raw
}

// getPage GETs url with header and returns a 2xx answer's body. 401 and
// 403 are usage.ErrAuth; a redirect is not followed and is a refusal too,
// since a dashboard sends a signed-out session to its sign-in page. Errors
// name the host and path, never the query or a header.
func getPage(ctx context.Context, hc *http.Client, url string, header http.Header) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[http.CanonicalHeaderKey(k)] = v
	}
	client := http.Client{Timeout: 20 * time.Second}
	if hc != nil {
		client = *hc
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("accounts: GET %s: %w", hostPath(url), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, hostPath(url))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, &statusError{method: http.MethodGet, where: hostPath(url), status: resp.StatusCode}
	}
	return body, nil
}
