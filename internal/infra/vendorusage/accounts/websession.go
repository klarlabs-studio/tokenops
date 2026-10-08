package accounts

import "strings"

// Helpers for the readers whose credential may be a pasted Cookie header or
// a bare session token. The request helpers (doWeb, cookieValue,
// browserUA) are in session.go.

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
