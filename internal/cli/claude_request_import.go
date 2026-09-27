package cli

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const requestImportInstructions = `Copy a content-free Claude usage request:
  1. open claude.ai/settings/usage in a desktop browser
  2. open Developer Tools -> Network and reload the page
  3. select the GET request ending /api/organizations/.../usage
  4. choose Copy -> Copy as cURL

TokenOps extracts only sessionKey, the allowlisted Cloudflare session cookies,
the browser identity headers needed to replay the request, and the organization
ID. It rejects any request outside claude.ai's usage endpoint and does not store
the copied command, authorization headers, request body, or response.`

// importedBrowserHeaders is deliberately closed. These fields describe the
// browser request shape Cloudflare evaluated; none carries account authority.
// Cookie and User-Agent have dedicated fields and are handled separately.
var importedBrowserHeaders = []string{
	"accept-language",
	"priority",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"sec-fetch-dest",
	"sec-fetch-mode",
	"sec-fetch-site",
}

var importedBrowserCookies = []string{"__cf_bm", "_cfuvid"}

var usageURLPattern = regexp.MustCompile(`https://claude\.ai/api/organizations/([^/?#[:space:]'"\\]+)/usage(?:[?#][^[:space:]'"\\]*)?`)

// parseUsageRequest reduces a browser's copied cURL command to the bounded
// authentication metadata needed by the meter. It deliberately does not use a
// shell parser or retain the command: copied requests can contain unrelated
// headers, and none of those belong in config or telemetry.
func parseUsageRequest(raw string) (browserSession, error) {
	raw = strings.TrimSpace(raw)
	match := usageURLPattern.FindStringSubmatch(raw)
	if len(match) != 2 {
		return browserSession{}, errors.New("copied request is not a claude.ai organization usage request; nothing was written")
	}
	parsed, err := url.Parse(match[0])
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "claude.ai" {
		return browserSession{}, errors.New("copied request must use https://claude.ai; nothing was written")
	}

	cookieLine := copiedHeader(raw, "cookie")
	if cookieLine == "" {
		cookieLine = copiedCookieFlag(raw)
	}
	cookies := parseCookieHeader(cookieLine)
	key := strings.TrimSpace(cookies["sessionKey"])
	if key == "" {
		return browserSession{}, errors.New("copied usage request has no sessionKey cookie; nothing was written")
	}
	headers := make(map[string]string)
	for _, name := range importedBrowserHeaders {
		if value := strings.TrimSpace(copiedHeader(raw, name)); validImportedHeader(value) {
			headers[name] = value
		}
	}
	browserCookies := make(map[string]string)
	for _, name := range importedBrowserCookies {
		if value := strings.TrimSpace(cookies[name]); value != "" {
			browserCookies[name] = value
		}
	}
	return browserSession{
		key:            key,
		clearance:      strings.TrimSpace(cookies["cf_clearance"]),
		userAgent:      strings.TrimSpace(copiedHeader(raw, "user-agent")),
		browserHeaders: headers,
		browserCookies: browserCookies,
		orgID:          match[1],
	}, nil
}

func validImportedHeader(value string) bool {
	return value != "" && len(value) <= 1024 && !strings.ContainsAny(value, "\r\n")
}

func copiedHeader(raw, name string) string {
	needle := strings.ToLower(name) + ":"
	for _, value := range copiedOptionValues(raw, "-H", "--header") {
		if strings.HasPrefix(strings.ToLower(value), needle) {
			return strings.TrimSpace(value[len(needle):])
		}
	}
	return ""
}

func copiedCookieFlag(raw string) string {
	values := copiedOptionValues(raw, "-b", "--cookie")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func copiedOptionValues(raw string, options ...string) []string {
	var values []string
	for _, option := range options {
		quoted := regexp.MustCompile(`(?:^|[[:space:]])` + regexp.QuoteMeta(option) + `[[:space:]]+(?:'([^']*)'|"([^"]*)"|([^[:space:]]+))`)
		for _, match := range quoted.FindAllStringSubmatch(raw, -1) {
			for i := 1; i < len(match); i++ {
				if match[i] != "" {
					values = append(values, match[i])
					break
				}
			}
		}
	}
	return values
}

func parseCookieHeader(value string) map[string]string {
	req := &http.Request{Header: http.Header{"Cookie": []string{value}}}
	out := make(map[string]string)
	for _, cookie := range req.Cookies() {
		out[cookie.Name] = cookie.Value
	}
	return out
}
