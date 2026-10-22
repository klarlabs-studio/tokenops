// Package errbody turns an upstream vendor's error answer into text
// that is safe to put in an error. Those errors reach GET /api/sources,
// the MCP data_sources view and the daemon log, so an upstream that
// echoes the request back (headers, cookies, a key in a form) must not
// carry the credential with it.
//
// The rules: a refusal (401, 403, 407) is described by its status
// alone, because its body is the likeliest to echo what was sent; any
// other body is redacted, folded onto one line and cut to
// MaxSnippetBytes.
package errbody

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.klarlabs.de/tokenops/internal/contexts/security/redaction"
)

// MaxReadBytes bounds how much of an error body Read buffers.
const MaxReadBytes = 4 << 10

// MaxSnippetBytes caps the quoted part of an error body.
const MaxSnippetBytes = 200

var redactor = redaction.Default()

// Read reads at most MaxReadBytes of an error body. Read errors are
// ignored: the body only ever enriches an error that already exists.
func Read(r io.Reader) []byte {
	b, _ := io.ReadAll(io.LimitReader(r, MaxReadBytes))
	return b
}

// Snippet is what of body may be quoted for an answer with this status:
// empty for a refusal, otherwise the body redacted, on one line, and at
// most MaxSnippetBytes (plus an ellipsis when cut).
func Snippet(status int, body []byte) string {
	if refusal(status) {
		return ""
	}
	red, _ := redactor.Redact(string(body))
	red = strings.Join(strings.Fields(red), " ")
	if len(red) <= MaxSnippetBytes {
		return red
	}
	cut := MaxSnippetBytes
	for cut > 0 && !utf8.RuneStart(red[cut]) {
		cut--
	}
	return red[:cut] + "…"
}

// Status describes an answer for an error message: "status 503", or
// "status 503: <snippet>" when the body has something safe to say.
func Status(status int, body []byte) string {
	s := "status " + strconv.Itoa(status)
	if sn := Snippet(status, body); sn != "" {
		s += ": " + sn
	}
	return s
}

func refusal(status int) bool {
	return status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		status == http.StatusProxyAuthRequired
}
