// Package providers describes the upstream LLM providers TokenOps proxies for.
//
// A Provider defines its TokenOps-side path prefix (under which the daemon
// hosts the reverse-proxy mount), the upstream base URL clients are forwarded
// to, and a canonical-request normalizer used by the event pipeline.
package providers

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Provider describes a single upstream LLM provider TokenOps proxies for.
type Provider struct {
	// ID is the canonical TokenOps identifier (matches eventschema.Provider).
	ID eventschema.Provider
	// Prefix is the local path prefix under which the proxy mounts this
	// provider, e.g. "/openai/". Always begins and ends with "/".
	Prefix string
	// DefaultBaseURL is the upstream base URL clients are forwarded to when
	// no override is supplied via configuration.
	DefaultBaseURL string
	// Normalize converts a raw request body into a CanonicalRequest. May
	// return ErrUnknownPath for paths that do not carry an inference
	// payload (e.g. listing endpoints, health checks).
	Normalize NormalizeFunc
}

// ErrUnknownPath signals that a request path does not have a known canonical
// shape; the proxy should still forward it but cannot extract event fields.
var ErrUnknownPath = errors.New("providers: unknown path")

// All known providers. Order is stable so callers can deterministically
// iterate (e.g. when building proxy routes).
func All() []Provider {
	return []Provider{
		openAIProvider,
		anthropicProvider,
		geminiProvider,
	}
}

// Lookup finds a Provider by its canonical ID. Returns false if absent.
func Lookup(id eventschema.Provider) (Provider, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// ResolveByPath returns the Provider whose Prefix matches the request path.
// The trailing string (after the prefix) is returned for downstream URL
// rewriting.
func ResolveByPath(path string) (Provider, string, bool) {
	if !strings.HasPrefix(path, "/") {
		return Provider{}, "", false
	}
	for _, p := range All() {
		if strings.HasPrefix(path, p.Prefix) {
			return p, strings.TrimPrefix(path, strings.TrimSuffix(p.Prefix, "/")), true
		}
	}
	return Provider{}, "", false
}

// ParseUpstream validates a base URL string and returns its parsed form. The
// URL must be absolute (scheme + host) and must not contain a query string;
// any path is preserved as the upstream root.
func ParseUpstream(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("upstream URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("upstream URL %q must include scheme and host", raw)
	}
	if u.RawQuery != "" {
		return nil, fmt.Errorf("upstream URL %q must not include query parameters", raw)
	}
	return u, nil
}
