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

// One host can bill two ways; the path decides, and the endpoint name
// says whether a plan can cover the turn.
func TestEndpointCatalogPaths(t *testing.T) {
	for _, tc := range []struct {
		url, provider, name string
	}{
		{"https://api.z.ai/api/anthropic", "zai", "zai"},
		{"https://api.z.ai/api/coding/paas/v4", "zai", "zai"},
		{"https://api.z.ai/api/paas/v4", "zai", "zai-api"},
		{"https://open.bigmodel.cn/api/anthropic", "zhipuai", "zhipuai"},
		{"https://opencode.ai/zen/go/v1", "opencode-go", "opencode-go"},
		{"https://opencode.ai/zen/v1", "opencode", "opencode"},
		{"https://api.kimi.com/coding/", "kimi", "kimi"},
		{"https://api.kimi.ai/coding/", "kimi", "kimi"},
		{"https://api.moonshot.ai/anthropic", "moonshot", "moonshot"},
		{"https://api.minimax.io/anthropic", "minimax", "minimax"},
		{"https://coding-intl.dashscope.aliyuncs.com/apps/anthropic", "alibaba", "alibaba"},
		{"https://api.deepseek.com/anthropic", "deepseek", "deepseek"},
	} {
		e, ok := EndpointFor(tc.url)
		if !ok || string(e.Provider) != tc.provider {
			t.Errorf("EndpointFor(%q) = %+v, %v; want provider %q", tc.url, e, ok, tc.provider)
			continue
		}
		if got := EndpointName(tc.url, "anthropic"); got != tc.name {
			t.Errorf("EndpointName(%q) = %q, want %q", tc.url, got, tc.name)
		}
	}
	// The coding plan covers its own endpoint; the pay-as-you-go API does not.
	if !PlanApplies("zai", EndpointName("https://api.z.ai/api/anthropic", "")) || PlanApplies("zai", EndpointName("https://api.z.ai/api/paas/v4", "")) {
		t.Error("z.ai plan coverage follows the path")
	}
}

// A non-Anthropic endpoint bills every turn it serves, even one it
// reports under a Claude name.
func TestNonAnthropicEndpointBillsClaudeNamedTurns(t *testing.T) {
	if got := ForClaudeCodeTurn("claude-opus-5-5", "https://api.deepseek.com/anthropic"); got != eventschema.ProviderDeepSeek {
		t.Errorf("DeepSeek's Anthropic API: %q", got)
	}
	if got := ForClaudeCodeTurn("glm-5.3", "https://api.z.ai/api/anthropic"); got != "zai" {
		t.Errorf("z.ai coding plan: %q", got)
	}
}
