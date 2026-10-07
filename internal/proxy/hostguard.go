package proxy

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// hostGuard keeps web pages from using the daemon.
//
// The provider routes are unauthenticated by design — clients send their own
// vendor keys — so anything that can make the daemon's listener accept a
// request can push traffic through it, and every such request is observed
// into spend and learning data. Two browser paths reach a loopback listener:
//
//   - a cross-site request: a page posts text/plain to
//     http://127.0.0.1:7878/anthropic/v1/messages, which CORS does not
//     preflight. The browser marks it with Origin and Sec-Fetch-Site.
//   - DNS rebinding: a page on attacker.example re-resolves its own name to
//     127.0.0.1 and becomes same-origin with the daemon. The browser still
//     sends Host: attacker.example, which no legitimate client does.
//
// So the guard admits a request only when its Host names this machine —
// loopback, the listen address, or a name the operator configured — and,
// when a browser says where it comes from, that origin is local too. CLI and
// SDK clients send neither Origin nor Sec-Fetch-Site and pass unchanged.
type hostGuard struct {
	// names are the lowercased hosts (names or IPs, no port) admitted
	// beyond loopback.
	names map[string]bool
	// anyIP admits every IP-literal Host. It is set when the daemon binds a
	// wildcard address, where peers reach it on whichever interface address
	// they know. A rebinding attack needs a DNS name, so IP literals are
	// safe to admit — but only as Host, never as Origin: a page served from
	// some other IP is still a foreign site.
	anyIP bool
}

// newHostGuard builds the guard for a daemon bound to listen, admitting the
// extra hosts the operator configured.
func newHostGuard(listen string, extra []string) hostGuard {
	g := hostGuard{names: map[string]bool{}}
	host := listen
	if h, _, err := net.SplitHostPort(listen); err == nil {
		host = h
	}
	host = normalizeHost(host)
	switch ip := net.ParseIP(host); {
	case host == "" || (ip != nil && ip.IsUnspecified()):
		g.anyIP = true
	default:
		g.names[host] = true
	}
	for _, h := range extra {
		if n := normalizeHost(stripPort(h)); n != "" {
			g.names[n] = true
		}
	}
	return g
}

// allowsHost reports whether a request's Host header names this daemon.
func (g hostGuard) allowsHost(hostport string) bool {
	if hostport == "" {
		// HTTP/1.0 clients may omit Host; browsers never do.
		return true
	}
	host := normalizeHost(stripPort(hostport))
	if g.isLocal(host) {
		return true
	}
	return g.anyIP && net.ParseIP(host) != nil
}

// allowsOrigin reports whether a browser Origin is this machine. The opaque
// "null" origin (sandboxed frames, file://) is never admitted.
func (g hostGuard) allowsOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return g.isLocal(normalizeHost(u.Hostname()))
}

// isLocal reports whether a normalised host is loopback or configured.
func (g hostGuard) isLocal(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return g.names[host]
}

// middleware rejects foreign requests with 403 before any route runs.
func (g hostGuard) middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isProbe(r) {
			next.ServeHTTP(w, r)
			return
		}
		if reason := g.reject(r); reason != "" {
			logger.Warn("request rejected: not from this machine",
				"reason", reason,
				"host", r.Host,
				"origin", r.Header.Get("Origin"),
				"path", r.URL.Path,
			)
			http.Error(w, "tokenops: "+reason, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// reject returns why a request is refused, or "" to admit it.
func (g hostGuard) reject(r *http.Request) string {
	if !g.allowsHost(r.Host) {
		return "host not allowed; add it to allowed_hosts to serve it"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if !g.allowsOrigin(origin) {
			return "cross-origin browser requests are not accepted"
		}
		// A local origin is trusted even when the browser calls it
		// cross-site (http://localhost:3000 → http://127.0.0.1:7878).
		return ""
	}
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return "cross-site browser requests are not accepted"
	}
	return ""
}

// isProbe reports a liveness/readiness/version read. Probes are answered
// whatever the Host — an orchestrator addresses the daemon by names the
// guard cannot know — and they reveal nothing a page could use.
func isProbe(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/healthz", "/readyz", "/version":
		return true
	}
	return false
}

// stripPort removes a :port suffix and IPv6 brackets.
func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
}

// normalizeHost lowercases a host and drops a trailing root dot.
func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}
