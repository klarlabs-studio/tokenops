package providers

//go:generate go run go.klarlabs.de/tokenops/internal/tools/gen/listgen -out registry_gen.go -list registered=provider:Descriptor
//go:generate go run go.klarlabs.de/tokenops/internal/tools/gen/providerdocs -root ../../../..

import (
	"slices"
	"strings"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// all is every descriptor, in registry_gen.go's order (by file name), with
// each plan's Provider filled in.
var all = build()

func build() []Descriptor {
	out := make([]Descriptor, 0, len(registered))
	for _, f := range registered {
		d := f()
		for i := range d.Plans {
			if d.Plans[i].Provider == "" {
				d.Plans[i].Provider = string(d.ID)
			}
		}
		out = append(out, d)
	}
	return out
}

// All returns every provider, ordered by ID. The slice is a copy; the
// descriptors share their inner slices and must not be modified.
func All() []Descriptor { return slices.Clone(all) }

// Lookup returns the provider with id.
func Lookup(id string) (Descriptor, bool) {
	for _, d := range all {
		if string(d.ID) == id {
			return d, true
		}
	}
	return Descriptor{}, false
}

// Kind is what the provider's first source reads, "" for a provider in
// the catalog only.
func (d Descriptor) Kind() Kind {
	if len(d.Sources) == 0 {
		return ""
	}
	return d.Sources[0].Kind
}

// Label is the provider's name in the docs tables.
func (d Descriptor) Label() string {
	if d.Docs.Label != "" {
		return d.Docs.Label
	}
	return d.DisplayName
}

// Setupable is the source `tokenops vendor-usage setup <id>` connects for
// this provider: an account reader read with a key (an API key or an
// organisation admin key), a browser session, another app's Keychain
// sign-in or the vendor's credential chain, or a gateway read with a key at
// the address the operator gives.
func (d Descriptor) Setupable() (Source, bool) {
	for _, s := range d.Sources {
		if s.Reader == AccountReader && (s.Credential == APIKey || s.Credential == AdminKey || s.Credential == BrowserCookie || s.Credential == PasswordLogin || s.Credential == CredentialChain || s.Credential == AppKeychain || s.Credential == AppLogin) {
			return s, true
		}
		if s.Reader == GatewayReader && s.Credential == APIKey {
			return s, true
		}
	}
	return Source{}, false
}

// SessionSource is the provider's account source read with a browser
// session, when it has one: setup reads the browser for it, and the daemon
// re-reads it quietly when the stored session is refused.
func (d Descriptor) SessionSource() (Source, bool) {
	for _, s := range d.Sources {
		if s.Reader == AccountReader && s.Credential == BrowserCookie && s.Cookie != nil {
			return s, true
		}
	}
	return Source{}, false
}

// AppLoginSource is the account source that can be read with another
// application's sign-in, once the operator granted it (ADR 0013).
func (d Descriptor) AppLoginSource() (Source, bool) {
	for _, s := range d.Sources {
		if s.Reader == AccountReader && len(s.AppLogins) > 0 {
			return s, true
		}
	}
	return Source{}, false
}

// SourceOf is a source with the provider it belongs to.
type SourceOf struct {
	Source
	Provider eventschema.Provider
}

// Sources returns every source of every provider, in registry order.
func Sources() []SourceOf {
	var out []SourceOf
	for _, d := range all {
		for _, s := range d.Sources {
			out = append(out, SourceOf{Source: s, Provider: d.ID})
		}
	}
	return out
}

// ForSource returns the source with tag and its provider.
func ForSource(tag string) (SourceOf, bool) {
	for _, s := range Sources() {
		if s.Tag == tag {
			return s, true
		}
	}
	return SourceOf{}, false
}

// SourceProviders maps each source tag to the provider it reports on.
// A source that reports on every provider (AnyProvider) is absent.
func SourceProviders() map[string]string {
	out := map[string]string{}
	for _, s := range Sources() {
		if !s.AnyProvider {
			out[s.Tag] = string(s.Provider)
		}
	}
	return out
}

// EndpointOf is an endpoint with the provider it bills to.
type EndpointOf struct {
	Endpoint
	Provider eventschema.Provider
}

// Endpoints returns every known endpoint.
func Endpoints() []EndpointOf {
	var out []EndpointOf
	for _, d := range all {
		for _, e := range d.Endpoints {
			out = append(out, EndpointOf{Endpoint: e, Provider: d.ID})
		}
	}
	return out
}

// OpencodeMapping is the provider and endpoint an opencode provider ID
// names.
type OpencodeMapping struct {
	Provider eventschema.Provider
	Endpoint string
}

// Opencode maps each opencode provider ID to the provider that bills it.
func Opencode() map[string]OpencodeMapping {
	out := map[string]OpencodeMapping{}
	for _, d := range all {
		for _, o := range d.Opencode {
			out[o.ID] = OpencodeMapping{Provider: d.ID, Endpoint: o.Endpoint}
		}
	}
	return out
}

// EnvVars maps each conventional key variable to the opencode provider ID
// of the vendor it belongs to.
func EnvVars() map[string]string {
	out := map[string]string{}
	for _, d := range all {
		if len(d.Opencode) == 0 {
			continue
		}
		for _, v := range d.EnvVars {
			out[v] = d.Opencode[0].ID
		}
	}
	return out
}

// OwnEnvVars maps each conventional key variable of a provider opencode
// does not know (no Opencode ID) to that provider's ID, the endpoint its
// account reader takes keys for.
func OwnEnvVars() map[string]string {
	out := map[string]string{}
	for _, d := range all {
		if len(d.Opencode) > 0 {
			continue
		}
		for _, v := range d.EnvVars {
			out[v] = string(d.ID)
		}
	}
	return out
}

// SourceEnv is where one source's own credential is found in the
// environment: the key in the first of Vars that is set and, for a
// gateway, its address.
type SourceEnv struct {
	Provider string
	Vars     []string
	// Gateway marks a gateway's source, read at BaseURLEnv's address or
	// DefaultBaseURL.
	Gateway        bool
	BaseURLEnv     string
	DefaultBaseURL string
}

// SourceEnvs lists every source with variables of its own (Source.EnvVars).
func SourceEnvs() []SourceEnv {
	var out []SourceEnv
	for _, d := range all {
		for _, s := range d.Sources {
			if len(s.EnvVars) == 0 {
				continue
			}
			out = append(out, SourceEnv{Provider: string(d.ID), Vars: s.EnvVars,
				Gateway: s.Reader == GatewayReader, BaseURLEnv: s.BaseURLEnv, DefaultBaseURL: s.DefaultBaseURL})
		}
	}
	return out
}

// SourceEnvVars maps each variable holding one source's own credential
// (Source.EnvVars) to the provider whose source it is.
func SourceEnvVars() map[string]string {
	out := map[string]string{}
	for _, e := range SourceEnvs() {
		for _, v := range e.Vars {
			out[v] = e.Provider
		}
	}
	return out
}

// ModelsDevPricing maps a models.dev provider ID to the providers its
// rates price.
func ModelsDevPricing() map[string][]string {
	out := map[string][]string{}
	for _, d := range all {
		for _, id := range d.ModelsDev {
			out[id] = append(out[id], string(d.ID))
		}
	}
	return out
}

// Plans returns every catalog plan of every provider.
func Plans() []Plan {
	var out []Plan
	for _, d := range all {
		out = append(out, d.Plans...)
	}
	return out
}

// DisplayName names a provider as its users do; a provider the registry
// does not know is capitalised.
func DisplayName(id string) string {
	if d, ok := Lookup(id); ok {
		return d.DisplayName
	}
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// PlanPrefixes are the vendor names plan display names start with.
func PlanPrefixes() []string {
	var out []string
	for _, d := range all {
		if d.PlanPrefix != "" {
			out = append(out, d.PlanPrefix)
		}
	}
	return out
}
