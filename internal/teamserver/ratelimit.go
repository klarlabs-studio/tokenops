package teamserver

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// limiter is a token bucket per key: a device, or a client address. One
// server process holds them in memory, which is what a single VPS runs.
type limiter struct {
	mu      sync.Mutex
	perSec  float64
	burst   float64
	buckets map[string]*tokenBucket
	now     func() time.Time
}

type tokenBucket struct {
	tokens float64
	at     time.Time
}

// maxKeys bounds the memory a flood of distinct keys can take; past it,
// buckets that have refilled are dropped.
const maxKeys = 10000

func newLimiter(every time.Duration, burst int) *limiter {
	return &limiter{perSec: 1 / every.Seconds(), burst: float64(burst), buckets: map[string]*tokenBucket{}, now: time.Now}
}

// Allow takes one token for key, reporting whether there was one.
func (l *limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxKeys {
			l.evict(now)
		}
		b = &tokenBucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.at).Seconds()*l.perSec)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *limiter) evict(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.at).Seconds()*l.perSec >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// clientIP is the address a request came from. Behind a trusted proxy
// (Caddy), it is the rightmost X-Forwarded-For entry the proxy did not add
// itself; otherwise the connection's peer.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !isTrusted(peer, trusted) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			break
		}
		if !isTrusted(a, trusted) {
			return a.String()
		}
	}
	return host
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}
