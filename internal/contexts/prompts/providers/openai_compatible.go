package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

// NewOpenAICompatible builds a Provider for an upstream that speaks the OpenAI
// /chat/completions wire format — the de-facto standard adopted by Groq,
// DeepSeek, xAI, Together, Fireworks, Cerebras, Perplexity, and OpenRouter.
// Only the identifier, path prefix, and upstream base URL differ; request
// normalization and token accounting are identical to OpenAI's, so a single
// call is enough to add first-class metering for one of these providers.
//
// Auth is passthrough (see registerProviderRoutes), so the client's own
// Authorization: Bearer header reaches the upstream unchanged — TokenOps
// stores no key for these providers.
func NewOpenAICompatible(id eventschema.Provider, prefix, baseURL string) Provider {
	return Provider{
		ID:             id,
		Prefix:         prefix,
		DefaultBaseURL: baseURL,
		Normalize:      normalizeOpenAICompatible(id),
	}
}

// The OpenAI-compatible provider fleet. Each DefaultBaseURL is the host root
// under which the upstream exposes its OpenAI-compatible surface; the client's
// trailing path (e.g. /v1/chat/completions) is joined onto it by the proxy
// director. Base URLs carry a path segment where the upstream nests its
// OpenAI surface (Groq /openai, Fireworks /inference, OpenRouter /api).
var (
	groqProvider       = NewOpenAICompatible(eventschema.ProviderGroq, "/groq/", "https://api.groq.com/openai")
	deepSeekProvider   = NewOpenAICompatible(eventschema.ProviderDeepSeek, "/deepseek/", "https://api.deepseek.com")
	xaiProvider        = NewOpenAICompatible(eventschema.ProviderXAI, "/xai/", "https://api.x.ai")
	perplexityProvider = NewOpenAICompatible(eventschema.ProviderPerplexity, "/perplexity/", "https://api.perplexity.ai")
	fireworksProvider  = NewOpenAICompatible(eventschema.ProviderFireworks, "/fireworks/", "https://api.fireworks.ai/inference")
	cerebrasProvider   = NewOpenAICompatible(eventschema.ProviderCerebras, "/cerebras/", "https://api.cerebras.ai")
	togetherProvider   = NewOpenAICompatible(eventschema.ProviderTogether, "/together/", "https://api.together.xyz")
	openRouterProvider = NewOpenAICompatible(eventschema.ProviderOpenRouter, "/openrouter/", "https://openrouter.ai/api")

	// Local runtimes + self-hosted gateways. Presets are each tool's
	// documented default endpoint; users override via `provider set` for a
	// non-default host/port. Local runtimes need no key (passthrough auth).
	ollamaProvider   = NewOpenAICompatible(eventschema.ProviderOllama, "/ollama/", "http://localhost:11434")
	lmStudioProvider = NewOpenAICompatible(eventschema.ProviderLMStudio, "/lmstudio/", "http://localhost:1234")
	liteLLMProvider  = NewOpenAICompatible(eventschema.ProviderLiteLLM, "/litellm/", "http://localhost:4000")
	vercelProvider   = NewOpenAICompatible(eventschema.ProviderVercel, "/vercel/", "https://ai-gateway.vercel.sh")
)
