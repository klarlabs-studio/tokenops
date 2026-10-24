package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
)

// ProviderRoute pairs a Provider with its configured upstream URL.
type ProviderRoute struct {
	Provider providers.Provider
	Upstream *url.URL
}

// BuildProviderRoutes constructs the default route table from the providers
// package, applying upstream overrides keyed by provider ID. Unknown override
// keys are returned as an error.
func BuildProviderRoutes(overrides map[string]string) ([]ProviderRoute, error) {
	routes := make([]ProviderRoute, 0, len(providers.All()))
	used := make(map[string]bool, len(overrides))
	for _, p := range providers.All() {
		raw := p.DefaultBaseURL
		if v, ok := overrides[string(p.ID)]; ok && v != "" {
			raw = v
			used[string(p.ID)] = true
		}
		u, err := providers.ParseUpstream(raw)
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", p.ID, err)
		}
		routes = append(routes, ProviderRoute{Provider: p, Upstream: u})
	}
	for k := range overrides {
		if !used[k] {
			return nil, fmt.Errorf("unknown provider override %q", k)
		}
	}
	return routes, nil
}

// registerProviderRoutes mounts a reverse proxy under each provider prefix.
// Inbound:  <prefix>/<rest...>  →  <upstream>/<rest...>
//
// Header passthrough: httputil.ReverseProxy strips hop-by-hop headers and
// forwards everything else, so provider-specific auth (Authorization,
// x-api-key, x-goog-api-key) is propagated unchanged.
func (s *Server) registerProviderRoutes(mux *http.ServeMux) error {
	for _, route := range s.routes {
		if err := s.mountReverseProxy(mux, route); err != nil {
			return err
		}
	}
	return nil
}

// tokenopsHeaderPrefix namespaces every header TokenOps reads from its
// clients (correlation ids, cache directives). They are TokenOps-local
// metadata and may carry personal data (a user id can be an email).
const tokenopsHeaderPrefix = "X-Tokenops-"

// stripTokenopsHeaders removes every TokenOps-namespaced header from an
// outbound request. It runs in the reverse proxy's Rewrite, so it applies
// to every provider route whether or not the observer, routing or cache
// middleware is mounted, and covers headers added later under the prefix.
func stripTokenopsHeaders(h http.Header) {
	for name := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), tokenopsHeaderPrefix) {
			delete(h, name)
		}
	}
}

func (s *Server) mountReverseProxy(mux *http.ServeMux, route ProviderRoute) error {
	upstream := route.Upstream
	prefix := route.Provider.Prefix

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetXForwarded()
			pr.Out.URL.Scheme = upstream.Scheme
			pr.Out.URL.Host = upstream.Host
			pr.Out.Host = upstream.Host

			// Strip the TokenOps prefix and prepend the upstream root path.
			rest := strings.TrimPrefix(pr.In.URL.Path, strings.TrimSuffix(prefix, "/"))
			pr.Out.URL.Path = singleJoin(upstream.Path, rest)
			pr.Out.URL.RawPath = ""
			stripTokenopsHeaders(pr.Out.Header)
		},
		// FlushInterval = -1 forces an immediate flush after every Write,
		// which is what SSE clients expect. Go's ReverseProxy auto-detects
		// "text/event-stream" since 1.17, but pinning the value makes the
		// behaviour explicit and survives content-type re-detection bugs.
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			meter := s.streamMeter.NewMeter(resp)
			if isStreamingResponse(resp) {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			resp.Body = newMeteredBody(resp.Body, meter)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.logger.Error("upstream error", "provider", route.Provider.ID, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}

	var handler http.Handler = rp
	if s.observerActive {
		// Routing rewrites run inside the observer so the observation
		// keeps the original requested model and the routing middleware
		// can read the canonicalised request from context.
		inner := handler
		if s.router != nil {
			inner = s.routingMiddleware(route.Provider, inner)
		}
		handler = s.observerMiddleware(route.Provider, inner)
	}
	if s.cache != nil {
		handler = s.cacheMiddleware(route.Provider, handler)
	}
	// Resilience wraps last so the breaker / streamtimeout observe the
	// full request including cache + observer overhead, and any
	// breaker-synthesised 503/504 isn't itself recorded as a successful
	// upstream call by the observer.
	if s.resilience != nil {
		mw, err := s.resilienceMiddleware(route)
		if err != nil {
			return err
		}
		handler = mw(handler)
	}
	mux.Handle(prefix, latchRequestEOF(handler))
	return nil
}

// latchRequestEOF keeps the reverse proxy's transport off the request
// body once it has read to the end. The transport forwards the body from
// its own goroutine and, after the last byte, reads once more to confirm
// EOF. An HTTP/1 server closes the body when the reply's header is
// written, so when the provider answered before that confirming read, the
// read failed with ErrBodyReadAfterClose, the transport took it for a
// write error and closed the upstream connection, cutting a streaming
// reply after its first event. Full duplex would avoid the close, but on
// Go 1.26 it also leaves a connection whose body read failed open for
// reuse.
func latchRequestEOF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = &eofLatchBody{ReadCloser: r.Body}
		}
		next.ServeHTTP(w, r)
	})
}

// eofLatchBody answers io.EOF without reading again once its body has
// returned io.EOF.
type eofLatchBody struct {
	io.ReadCloser
	eof bool
}

func (b *eofLatchBody) Read(p []byte) (int, error) {
	if b.eof {
		return 0, io.EOF
	}
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.eof = true
	}
	return n, err
}

// singleJoin merges base and tail with exactly one separating "/" while
// preserving an absolute leading slash.
func singleJoin(base, tail string) string {
	switch {
	case base == "" || base == "/":
		if !strings.HasPrefix(tail, "/") {
			return "/" + tail
		}
		return tail
	case strings.HasSuffix(base, "/") && strings.HasPrefix(tail, "/"):
		return base + strings.TrimPrefix(tail, "/")
	case !strings.HasSuffix(base, "/") && !strings.HasPrefix(tail, "/"):
		return base + "/" + tail
	default:
		return base + tail
	}
}
