package proxy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/proxy/cache"
	"github.com/felixgeelhaar/tokenops/internal/providers"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Cache directives advertised on the request side. Values are matched
// case-insensitively. The header is namespaced under X-TokenOps so it
// never collides with provider-specific cache headers.
const (
	headerCacheControl = "X-Tokenops-Cache"
	headerCacheStatus  = "X-Tokenops-Cache-Status"

	cacheDirectiveBypass  = "bypass"
	cacheDirectiveRefresh = "refresh"

	cacheStatusHit     = "hit"
	cacheStatusMiss    = "miss"
	cacheStatusBypass  = "bypass"
	cacheStatusRefresh = "refresh"
	cacheStatusStore   = "store"
)

// cacheKey hashes the dimensions that identify a cacheable request:
// provider id + method + canonical path + body. Using SHA-256 keeps key
// collisions astronomically rare while staying cheap (~µs per request).
func cacheKey(provider providers.Provider, r *http.Request, body []byte) string {
	h := sha256.New()
	h.Write([]byte(provider.ID))
	h.Write([]byte{0})
	h.Write([]byte(r.Method))
	h.Write([]byte{0})
	h.Write([]byte(r.URL.Path))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// cacheMiddleware wraps next so cacheable requests can short-circuit on
// hit and recordable responses get written back to the cache on miss.
// Pass-through is the default when s.cache is nil.
func (s *Server) cacheMiddleware(provider providers.Provider, next http.Handler) http.Handler {
	if s.cache == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directive := strings.ToLower(strings.TrimSpace(r.Header.Get(headerCacheControl)))
		if directive == cacheDirectiveBypass {
			s.cache.MarkBypass()
			w.Header().Set(headerCacheStatus, cacheStatusBypass)
			next.ServeHTTP(w, r)
			return
		}
		// Only POSTs (chat completions, messages, generateContent) carry
		// the deterministic body the cache keys on. GETs flow through
		// untouched.
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		body, err := captureRequestBody(r)
		if err != nil || len(body) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		key := cacheKey(provider, r, body)

		if directive != cacheDirectiveRefresh {
			if entry, ok := s.cache.Get(key); ok {
				w.Header().Set(headerCacheStatus, cacheStatusHit)
				writeCachedResponse(w, entry)
				s.emitCacheHitEvent(r, provider, body, entry)
				return
			}
		}

		rec := newRecordingResponseWriter(w)
		statusValue := cacheStatusMiss
		if directive == cacheDirectiveRefresh {
			statusValue = cacheStatusRefresh
		}
		rec.Header().Set(headerCacheStatus, statusValue)
		next.ServeHTTP(rec, r)

		if !rec.shouldCache() {
			return
		}
		entry := &cache.Entry{
			Status:      rec.status,
			Headers:     cloneHeader(rec.Header()),
			Body:        append([]byte(nil), rec.buf.Bytes()...),
			ContentType: rec.Header().Get("Content-Type"),
			StoredAt:    time.Now().UTC(),
		}
		s.cache.Put(key, entry)
		// Surface the store outcome so observability tooling can spot
		// whether a miss actually populated the cache.
		w.Header().Set(headerCacheStatus, cacheStatusStore)
	})
}

// writeCachedResponse copies entry into w. Hop-by-hop headers and any
// Content-Length already set by entry are preserved so the client sees
// a byte-identical replay.
func writeCachedResponse(w http.ResponseWriter, entry *cache.Entry) {
	for k, vals := range entry.Headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if entry.Status == 0 {
		entry.Status = http.StatusOK
	}
	w.WriteHeader(entry.Status)
	_, _ = w.Write(entry.Body)
}

func cloneHeader(h http.Header) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		dup := make([]string, len(v))
		copy(dup, v)
		out[k] = dup
	}
	return out
}

// emitCacheHitEvent publishes a PromptEvent with CacheHit=true so the
// observability pipeline records cache savings the same way as upstream
// completions. When no bus is configured the call is a no-op.
func (s *Server) emitCacheHitEvent(r *http.Request, provider providers.Provider, body []byte, entry *cache.Entry) {
	if s.bus == nil {
		return
	}
	now := time.Now().UTC()
	sum := sha256.Sum256(body)
	pe := &eventschema.PromptEvent{
		PromptHash:    "sha256:" + hex.EncodeToString(sum[:]),
		Provider:      provider.ID,
		ResponseModel: "",
		Status:        entry.Status,
		Latency:       0,
		CacheHit:      true,
		WorkflowID:    r.Header.Get(headerWorkflowID),
		AgentID:       r.Header.Get(headerAgentID),
		SessionID:     r.Header.Get(headerSessionID),
		UserID:        r.Header.Get(headerUserID),
	}
	if provider.Normalize != nil {
		rest := strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(provider.Prefix, "/"))
		if canonical, err := provider.Normalize(rest, body); err == nil {
			pe.RequestModel = canonical.Model
			pe.MaxOutputTokens = canonical.MaxOutputTokens
		}
	}
	if s.tokenizer != nil {
		if n, err := s.tokenizer.PreflightCount(provider.ID, body); err == nil {
			pe.InputTokens = int64(n)
			pe.ContextSize = int64(n)
		}
		if n, err := s.tokenizer.CountText(provider.ID, string(entry.Body)); err == nil {
			pe.OutputTokens = int64(n)
		}
	}
	pe.TotalTokens = pe.InputTokens + pe.OutputTokens
	s.bus.Publish(&eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     now,
		Source:        s.source,
		Payload:       pe,
	})
}

// --- recording response writer ----------------------------------------

// recordingResponseWriter buffers the upstream response body so we can
// store it after the request completes. It also passes every byte
// through to the inner ResponseWriter so the client experiences the same
// streaming behaviour as a non-cached response.
//
// The recorder bails out of caching when it observes a streaming response
// (text/event-stream) or an error status; shouldCache() exposes the
// decision after the body completes.
type recordingResponseWriter struct {
	inner       http.ResponseWriter
	buf         bytes.Buffer
	status      int
	headerSent  bool
	streaming   bool
	overflowed  bool
	maxBufBytes int
}

func newRecordingResponseWriter(w http.ResponseWriter) *recordingResponseWriter {
	return &recordingResponseWriter{
		inner:       w,
		status:      http.StatusOK,
		maxBufBytes: 4 * 1024 * 1024, // 4MB cap mirrors cache.Options.MaxEntryBytes
	}
}

func (r *recordingResponseWriter) Header() http.Header { return r.inner.Header() }

func (r *recordingResponseWriter) WriteHeader(code int) {
	if r.headerSent {
		return
	}
	r.headerSent = true
	r.status = code
	ct := strings.ToLower(strings.TrimSpace(r.inner.Header().Get("Content-Type")))
	if strings.HasPrefix(ct, "text/event-stream") {
		r.streaming = true
	}
	r.inner.WriteHeader(code)
}

func (r *recordingResponseWriter) Write(b []byte) (int, error) {
	if !r.headerSent {
		r.WriteHeader(http.StatusOK)
	}
	if !r.streaming && !r.overflowed {
		if r.buf.Len()+len(b) > r.maxBufBytes {
			r.overflowed = true
			r.buf.Reset()
		} else {
			r.buf.Write(b)
		}
	}
	return r.inner.Write(b)
}

// Flush forwards to the underlying writer when supported. ReverseProxy
// installs http.Flusher so SSE clients see chunk-by-chunk delivery.
func (r *recordingResponseWriter) Flush() {
	if f, ok := r.inner.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying writer if it supports it. The proxy
// does not currently hijack but we proxy the surface so future upgrades
// (websockets) keep working.
func (r *recordingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.inner.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("proxy: response does not support hijacking")
}

// shouldCache reports whether the recorded response can be persisted.
// We refuse: streaming bodies, non-2xx, oversize bodies, and errors that
// short-circuited the writer.
func (r *recordingResponseWriter) shouldCache() bool {
	if r.streaming || r.overflowed {
		return false
	}
	if r.status < 200 || r.status >= 300 {
		return false
	}
	if r.buf.Len() == 0 {
		return false
	}
	return true
}
