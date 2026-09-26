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

TokenOps extracts only sessionKey, cf_clearance, User-Agent, and the organization
ID. It rejects any request outside claude.ai's usage endpoint and does not store
the copied command or response.`

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
	return browserSession{
		key:       key,
		clearance: strings.TrimSpace(cookies["cf_clearance"]),
		userAgent: strings.TrimSpace(copiedHeader(raw, "user-agent")),
		orgID:     match[1],
	}, nil
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
