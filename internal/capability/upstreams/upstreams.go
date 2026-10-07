// Package upstreams is the catalog of provider upstreams the daemon's
// proxy can forward to: each provider's ID and default base URL, and the
// check a URL the operator binds must pass. `tokenops provider` reads it.
package upstreams

import (
	"net/url"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Provider is one upstream preset: its ID and default base URL.
type Provider = providers.Provider

// All is every preset, in the catalog's order.
func All() []Provider { return providers.All() }

// Lookup is the preset for id.
func Lookup(id eventschema.Provider) (Provider, bool) { return providers.Lookup(id) }

// ParseUpstream checks a base URL the operator binds: absolute, http or
// https, with a host.
func ParseUpstream(raw string) (*url.URL, error) { return providers.ParseUpstream(raw) }
