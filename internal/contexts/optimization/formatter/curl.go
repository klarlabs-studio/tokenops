package formatter

import (
	"fmt"
	"strings"
)

// Curl compresses the output of `curl -v` (verbose mode). In verbose mode
// curl prefixes each line by role: "* " marks connection/TLS trace, "> "
// marks request headers curl sent, and "< " marks response headers curl
// received. The response body itself carries no prefix.
//
// The response status line ("< HTTP/…"), curl's own "curl: (N) …" error
// lines, and the fatal connection diagnostics ("* SSL certificate problem",
// "* Could not resolve host", any line carrying "error") are the signal an
// agent acts on and are always kept.
//
// At Balanced the connection/TLS trace ("* Trying…", "* TLSv1.3 (OUT)…",
// "* subject:", "* ALPN…", "* Server certificate:") is stripped — except the
// single useful "* Connected to" line and any critical "* " line — and the
// request-header echo ("> GET …", "> Host: …") is dropped too, since the
// agent almost always cares about the response rather than the request it
// just made. The response status and response headers ("< …") and the body
// survive. At Aggressive the non-status response headers ("< " lines that are
// not "< HTTP") are also dropped, leaving only the status line, the body, and
// the criticals.
//
// Output that does not resemble `curl -v` is handed to the generic noise
// scrub, so the formatter is never destructive on commands it does not model.
type Curl struct{}

// NewCurl returns the curl formatter.
func NewCurl() *Curl { return &Curl{} }

// Command reports the curl command token.
func (c *Curl) Command() string { return "curl" }

// CriticalLine treats the response status and any error diagnostic as
// critical: the response status line ("< HTTP/…"), curl's "curl: (N) …"
// error lines, the fatal "* SSL certificate problem" / "* Could not resolve
// host" trace lines, and any line carrying "error". The unprefixed response
// body is not classified critical here — it is kept by default as non-noise,
// just not force-preserved.
func (c *Curl) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "< HTTP"),
		strings.HasPrefix(t, "curl: ("),
		strings.HasPrefix(t, "* SSL certificate problem"),
		strings.HasPrefix(t, "* Could not resolve host"),
		strings.Contains(t, "error"):
		return true
	}
	return false
}

// looksLikeCurl reports whether b resembles `curl -v` verbose output.
func looksLikeCurl(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "* Trying") ||
		strings.Contains(s, "* Connected to") ||
		strings.Contains(s, "< HTTP") ||
		strings.Contains(s, "> GET") ||
		strings.Contains(s, "> POST") ||
		strings.Contains(s, "curl:") ||
		strings.Contains(s, "* SSL") ||
		strings.Contains(s, "* TLSv")
}

// Format compresses curl output; non-curl output falls back to generic.
func (c *Curl) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeCurl(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "curl: non-curl output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(c, raw, scrubbed, 0, "curl: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if c.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		// Connection/TLS trace: drop every "* " line except the one useful
		// "* Connected to" line (criticals were already kept above).
		if strings.HasPrefix(t, "* ") {
			if strings.HasPrefix(t, "* Connected to") {
				kept = append(kept, line)
			} else {
				dropped++
			}
			continue
		}
		// Request-header echo: the agent cares about the response, so drop it.
		if strings.HasPrefix(t, "> ") {
			dropped++
			continue
		}
		// Response headers: keep at Balanced. At Aggressive drop the
		// non-status headers, keeping only the status line (a critical, so
		// already handled) plus body.
		if strings.HasPrefix(t, "< ") {
			if level == LossAggressive {
				dropped++
				continue
			}
			kept = append(kept, line)
			continue
		}
		if t == "" {
			dropped++
			continue
		}
		// Unprefixed response body.
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("curl: %s, %d lines dropped", level, dropped)
	res := enforceCritical(c, raw, compact, dropped, notes)
	return res, true
}
