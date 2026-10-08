package providers

func providerV0() Descriptor {
	return Descriptor{
		ID:          "v0",
		DisplayName: "v0",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/V0,
			// Resources/Plugins/v0.ts, docs/v0.md; v0's Platform API reference
			// (https://v0.app/docs/api/v1/reference/user/get-billing).
			{Name: "v0_account", Tag: "v0-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/user/billing`, `GET /v1/rate-limits`",
				Shows:    "the billing cycle's balance used and the request quota used",
				Scope:    "a v0 project ID or slug, read instead of the key's default scope (sent as `?scope=`)"},
		},
		Endpoints: []Endpoint{{Host: "api.v0.dev", Billing: Direct}},
		Opencode:  []OpencodeID{{ID: "v0", Endpoint: "v0"}},
		EnvVars:   []string{"V0_API_KEY"},
		ModelsDev: []string{"v0"},
	}
}
