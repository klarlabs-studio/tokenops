package providers

func providerDevPass() Descriptor {
	return Descriptor{
		ID:          "devpass",
		DisplayName: "DevPass",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/DevPass,
			// Resources/Plugins/devpass.ts, docs/devpass.md; LLM Gateway's
			// https://docs.llmgateway.io/developers/devpass-usage.
			{Name: "devpass_account", Tag: "devpass-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/key`",
				Shows:    "the cycle's plan credits and the premium weekly window; on pay-as-you-go, the key's spend against its limit"},
		},
		// DevPass is LLM Gateway's coding plan, served on its gateway.
		Endpoints: []Endpoint{{Host: "api.llmgateway.io", Billing: Reseller, Source: "https://docs.llmgateway.io/developers/devpass-usage"}},
		Opencode:  []OpencodeID{{ID: "llmgateway", Endpoint: "devpass"}},
		EnvVars:   []string{"DEVPASS_API_KEY", "LLMGATEWAY_API_KEY"},
		ModelsDev: []string{"llmgateway"},
		Docs:      Docs{Label: "DevPass (LLM Gateway)"},
	}
}
