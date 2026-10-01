package biller

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestForClaudeCodeTurn(t *testing.T) {
	const fireworks = "https://api.fireworks.ai/inference"
	for _, tc := range []struct {
		model, baseURL string
		want           eventschema.Provider
	}{
		// Anthropic's default endpoint.
		{"claude-opus-5-5", "", eventschema.ProviderAnthropic},
		{"claude-sonnet-5", "https://api.anthropic.com", eventschema.ProviderAnthropic},
		// FireRouter: open models are Fireworks', Claude runs on the
		// operator's Anthropic credential and Anthropic bills it.
		{"kimi-k3", fireworks, eventschema.ProviderFireworks},
		{"glm-5p3-flash", fireworks, eventschema.ProviderFireworks},
		{"accounts/fireworks/models/glm-5p1", fireworks, eventschema.ProviderFireworks},
		{"claude-opus-5-5", fireworks, eventschema.ProviderAnthropic},
		// A reseller bills everything it serves, Claude included.
		{"anthropic/claude-sonnet-5", "https://openrouter.ai/api", eventschema.ProviderOpenRouter},
		// No endpoint known: the namespace decides, else unknown — never
		// Anthropic for a model Anthropic does not serve.
		{"accounts/fireworks/routers/glm-fast-latest[1m]", "", eventschema.ProviderFireworks},
		{"kimi-k3", "", eventschema.ProviderUnknown},
		{"kimi-k3", "http://127.0.0.1:7878/anthropic", eventschema.ProviderUnknown},
	} {
		if got := ForClaudeCodeTurn(tc.model, tc.baseURL); got != tc.want {
			t.Errorf("ForClaudeCodeTurn(%q, %q) = %q, want %q", tc.model, tc.baseURL, got, tc.want)
		}
	}
}

func TestIsClaude(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-opus-5-5[1m]":       true,
		"anthropic/claude-sonnet-5": true,
		"Claude-Haiku-4-5":          true,
		"glm-5p3":                   false,
		"":                          false,
	} {
		if got := IsClaude(model); got != want {
			t.Errorf("IsClaude(%q) = %v", model, got)
		}
	}
}

func TestForCodexTurn(t *testing.T) {
	const fw = "https://api.fireworks.ai/inference/v1"
	for _, tc := range []struct {
		id, base, model string
		want            eventschema.Provider
	}{
		{"openai", "", "gpt-6-sol", eventschema.ProviderOpenAI},
		{"", "", "gpt-6-sol", eventschema.ProviderOpenAI},
		{"fireworks", fw, "glm-5p3", eventschema.ProviderFireworks},
		{"fireworks", fw, "gpt-6-sol", eventschema.ProviderOpenAI}, // FireRouter on the operator's OpenAI key
		{"openrouter", "https://openrouter.ai/api/v1", "openai/gpt-6-sol", eventschema.ProviderOpenRouter},
		{"zai", "https://unknown.example/v4", "glm-5.3", "zai"}, // the operator's own label, not a guess
	} {
		if got := ForCodexTurn(tc.id, tc.base, tc.model); got != tc.want {
			t.Errorf("ForCodexTurn(%q, %q, %q) = %q, want %q", tc.id, tc.base, tc.model, got, tc.want)
		}
	}
}
