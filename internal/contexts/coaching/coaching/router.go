package coaching

import (
	"errors"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// BackendChoice describes which LLM backend the coaching pipeline
// should run summaries through. The shape is deliberately small: the
// pipeline-level wiring decides how to instantiate an llm.Backend from
// it (the coaching package can't import internal/contexts/prompts/llm
// directly without creating a cycle through eventschema).
type BackendChoice struct {
	// Kind is the backend transport: "anthropic", "openai", "gemini",
	// "github" (Copilot), "cursor", or "local" (Ollama).
	Kind string
	// Provider is the eventschema.Provider that matches Kind for cost
	// attribution. "" when local.
	Provider eventschema.Provider
	// Model is the discovered cheapest model id for the provider, or
	// the local default when Kind=local.
	Model string
	// Endpoint is the API base URL the backend should POST to. Empty
	// for vendor defaults; set explicitly for openai-compat / local.
	Endpoint string
	// CredentialSource hints at where the runtime should pull a key:
	// "plan_session" — reuse the user's configured plan credentials;
	// "env:<NAME>" — read from an environment variable; "none" — no
	// auth needed (typical for Ollama).
	CredentialSource string
	// Reason is a short string the operator sees when the choice is
	// logged. Helps explain "why this model" without grepping code.
	Reason string
}

// ErrNoBackend signals the router could not find any viable LLM
// backend (no plans configured, no local Ollama reachable, no fallback
// available). Coaching falls back to heuristic-only output in that
// case.
var ErrNoBackend = errors.New("coaching: no viable LLM backend")

// LocalProbe reports whether a local backend (e.g. Ollama) is
// reachable. Inject from the runtime so tests don't poke the network.
type LocalProbe func() (model string, ok bool)

// RouterInputs is the pure-function input the router needs to decide
// where coaching summaries should be generated.
type RouterInputs struct {
	// Plans is the user's configured provider→plan map (Config.Plans).
	Plans map[string]string
	// PricingTable is the spend table used to discover the cheapest
	// model per provider. Pass spend.DefaultTable() in production.
	PricingTable spend.Table
	// LocalProbe, when non-nil, is invoked as a last-resort check for
	// a reachable local backend (e.g. Ollama). Nil disables the
	// fallback.
	LocalProbe LocalProbe
}

// providerOrder is the preference order when multiple plans exist.
// Anthropic first because Claude Code is the dominant MCP host today;
// openai second; everything else last. The router still picks the
// cheapest available — this only breaks ties when the user has both
// Anthropic and OpenAI plans configured.
var providerOrder = []eventschema.Provider{
	eventschema.ProviderAnthropic,
	eventschema.ProviderOpenAI,
	eventschema.ProviderGemini,
}

// RouteBackend returns the best BackendChoice for the given inputs.
//
// Algorithm:
//  1. For each configured plan in preference order, look up the
//     cheapest model for the matching provider in the pricing table.
//     First hit wins.
//  2. If no plan resolves to a known provider, try LocalProbe.
//  3. If LocalProbe returns nothing, return ErrNoBackend.
//
// No model names are hardcoded — the router consults spend.Table for
// every choice so adding a new model to the table makes it
// automatically eligible.
func RouteBackend(in RouterInputs) (BackendChoice, error) {
	// Index the configured plans by provider for ordered traversal.
	configured := map[eventschema.Provider]string{}
	for providerName, planName := range in.Plans {
		configured[eventschema.Provider(providerName)] = planName
	}

	for _, provider := range providerOrder {
		planName, ok := configured[provider]
		if !ok {
			continue
		}
		model, _, err := in.PricingTable.Cheapest(provider)
		if err != nil {
			// Provider has a plan binding but no priced models —
			// pricing table needs a row added. Skip rather than fail
			// the entire route.
			continue
		}
		return BackendChoice{
			Kind:             string(provider),
			Provider:         provider,
			Model:            model,
			Endpoint:         vendorEndpoint(provider),
			CredentialSource: "plan_session",
			Reason:           "cheapest model on the configured " + planName + " plan",
		}, nil
	}

	// Configured provider had no priced model in the table — also try
	// any other configured provider we don't explicitly order.
	for provider := range configured {
		if isOrderedProvider(provider) {
			continue
		}
		model, _, err := in.PricingTable.Cheapest(provider)
		if err == nil {
			return BackendChoice{
				Kind:             string(provider),
				Provider:         provider,
				Model:            model,
				Endpoint:         vendorEndpoint(provider),
				CredentialSource: "plan_session",
				Reason:           "cheapest model on the configured " + configured[provider] + " plan",
			}, nil
		}
	}

	if in.LocalProbe != nil {
		if model, ok := in.LocalProbe(); ok {
			return BackendChoice{
				Kind:             "local",
				Model:            model,
				Endpoint:         "http://127.0.0.1:11434",
				CredentialSource: "none",
				Reason:           "no plans configured; reachable local Ollama",
			}, nil
		}
	}
	return BackendChoice{}, ErrNoBackend
}

func isOrderedProvider(p eventschema.Provider) bool {
	for _, op := range providerOrder {
		if op == p {
			return true
		}
	}
	return false
}

// vendorEndpoint returns the canonical API base URL for a provider.
// Empty string means "use the SDK / library default" (some backends
// resolve it themselves).
func vendorEndpoint(p eventschema.Provider) string {
	switch p {
	case eventschema.ProviderAnthropic:
		return "https://api.anthropic.com"
	case eventschema.ProviderOpenAI:
		return "https://api.openai.com"
	case eventschema.ProviderGemini:
		return "https://generativelanguage.googleapis.com"
	default:
		return ""
	}
}
