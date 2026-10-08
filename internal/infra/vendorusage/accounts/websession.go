package accounts

import (
	"strings"
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
