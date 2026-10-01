// Package biller decides whose bill a request counts against (ADR 0009).
//
// A harness's provider and its biller used to be the same thing. Claude
// Code spoke only Anthropic's API, so every Claude Code turn was billed to
// Anthropic. Gateways ended that: with Claude Code pointed at Fireworks,
// one session holds turns Fireworks serves and bills (kimi-k3, glm-5p3)
// and, through FireRouter, Claude turns run on the operator's own
// Anthropic credential and billed by Anthropic. Counting the Fireworks
// turns as Anthropic's put them against the wrong plan or spend limit,
// with no price.
package biller

import (
	"net/url"
	"strings"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Kind says how an endpoint bills the models it serves.
type Kind int

const (
	// Direct is the model vendor's own API: it bills what it serves.
	Direct Kind = iota
	// Reseller serves and bills every model, closed ones included
	// (OpenRouter passes Claude through on its own account).
	Reseller
	// OwnCredential serves and bills its own models, and runs closed
	// models on the operator's credential with their vendor, which bills
	// those turns (Fireworks FireRouter).
	OwnCredential
)

// Endpoint is a known API host and how it bills.
type Endpoint struct {
	Host     string
	Provider eventschema.Provider
	Kind     Kind
	// Source pins where the billing behaviour is documented.
	Source string
}

// endpoints are the hosts whose billing is known. A host not listed here
// is reported as unknown rather than guessed.
var endpoints = []Endpoint{
	{Host: "api.anthropic.com", Provider: eventschema.ProviderAnthropic, Kind: Direct},
	{Host: "api.fireworks.ai", Provider: eventschema.ProviderFireworks, Kind: OwnCredential,
		Source: "https://docs.fireworks.ai/nexus/firerouter (2026-10-01): closed models run on your own Anthropic or OpenAI account"},
	{Host: "openrouter.ai", Provider: eventschema.ProviderOpenRouter, Kind: Reseller},
	{Host: "api.together.xyz", Provider: eventschema.ProviderTogether, Kind: Reseller},
}

// EndpointFor returns the known endpoint a base URL points at. A local
// address is the TokenOps proxy or another local relay, which forwards
// to Anthropic; it is reported as unknown here, and model rules decide.
func EndpointFor(baseURL string) (Endpoint, bool) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Hostname() == "" {
		return Endpoint{}, false
	}
	host := strings.ToLower(u.Hostname())
	for _, e := range endpoints {
		if host == e.Host || strings.HasSuffix(host, "."+e.Host) {
			return e, true
		}
	}
	return Endpoint{}, false
}

// ForClaudeCodeTurn is the biller of a Claude Code turn, given the model
// that served it (from the transcript) and the base URL Claude Code was
// pointed at (empty for Anthropic's default).
//
//   - A Claude model is billed by Anthropic, unless a reseller served it.
//   - Any other model is never Anthropic's: Anthropic does not serve it.
//     The endpoint names the biller; failing that, a namespaced model ID
//     (accounts/fireworks/…) does; failing both, it is unknown.
func ForClaudeCodeTurn(model, baseURL string) eventschema.Provider {
	e, known := EndpointFor(baseURL)
	if known && e.Kind == Reseller {
		return e.Provider
	}
	if IsClaude(model) {
		return eventschema.ProviderAnthropic
	}
	if known && e.Provider != eventschema.ProviderAnthropic {
		return e.Provider
	}
	if p, ok := namespaced(model); ok {
		return p
	}
	return eventschema.ProviderUnknown
}

// IsClaude reports whether a served model is one of Anthropic's, under any
// namespace a gateway adds ("anthropic/claude-…", "claude-opus-5-5[1m]").
func IsClaude(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return strings.HasPrefix(m, "claude")
}

// namespaced reads the biller from a gateway's own model namespace.
func namespaced(model string) (eventschema.Provider, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "accounts/fireworks/"), strings.HasPrefix(m, "fireworks_ai/"), strings.HasPrefix(m, "firerouter"):
		return eventschema.ProviderFireworks, true
	}
	return "", false
}

// ForCodexTurn is the biller of a Codex turn, given the session's
// model_provider (from its rollout's session_meta), the base URL Codex's
// config gives that provider, and the model that served it.
//
//   - Codex's built-in "openai" provider is OpenAI's.
//   - A known gateway bills its own models; a reseller bills everything;
//     a gateway running closed models on the operator's credential leaves
//     an OpenAI model with OpenAI.
//   - Any other provider is named by its Codex provider ID, which is the
//     operator's own label for it, rather than guessed.
func ForCodexTurn(providerID, baseURL, model string) eventschema.Provider {
	id := strings.ToLower(strings.TrimSpace(providerID))
	if id == "" || id == "openai" {
		return eventschema.ProviderOpenAI
	}
	if e, ok := EndpointFor(baseURL); ok {
		if e.Kind == OwnCredential && isOpenAIModel(model) {
			return eventschema.ProviderOpenAI
		}
		return e.Provider
	}
	return eventschema.Provider(id)
}

// isOpenAIModel reports whether a served model is one of OpenAI's.
func isOpenAIModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4")
}
