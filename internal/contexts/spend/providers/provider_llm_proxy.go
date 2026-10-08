package providers

func providerLLMProxy() Descriptor {
	return Descriptor{
		ID:          "llm-proxy",
		DisplayName: "LLM Proxy",
		Sources: []Source{
			// LLM-API-Key-Proxy, self-hosted: it rotates the operator's own
			// provider credentials, so it bills nothing itself.
			{Name: "llm_proxy_gateway", Tag: "llm-proxy-account", Kind: Gateway, Credential: APIKey,
				Switch: SwitchAccounts, Reader: GatewayReader, Verified: FromCodexBar,
				Reference:    "CodexBar Sources/CodexBarCore/Resources/Plugins/llmproxy.ts (docs/llm-proxy.md)",
				EnvVars:      []string{"LLM_PROXY_API_KEY"},
				BaseURLEnv:   "LLM_PROXY_BASE_URL",
				RecognisedBy: "`GET /`, or its name (`LLM_PROXY_BASE_URL`, setup)",
				Endpoint:     "`GET /v1/quota-stats`",
				Shows:        "the tightest quota group left on the credentials it pools, and when it resets"},
		},
		Docs: Docs{Label: "LLM Proxy (LLM-API-Key-Proxy)"},
	}
}
