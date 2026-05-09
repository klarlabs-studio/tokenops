package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/providers"
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
func (s *Server) registerProviderRoutes(mux *http.ServeMux) {
	for _, route := range s.routes {
		s.mountReverseProxy(mux, route)
	}
}

func (s *Server) mountReverseProxy(mux *http.ServeMux, route ProviderRoute) {
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
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.logger.Error("upstream error", "provider", route.Provider.ID, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}

	mux.Handle(prefix, rp)
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
