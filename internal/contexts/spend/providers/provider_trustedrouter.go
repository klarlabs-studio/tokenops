package providers

func providerTrustedRouter() Descriptor {
	return Descriptor{
		ID:          "trustedrouter",
		DisplayName: "TrustedRouter",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/trustedrouter.ts,
			// Providers/TrustedRouter, docs/trustedrouter.md (a26ca57b). Not
			// verified against a live account.
			{Name: "trustedrouter_account", Tag: "trustedrouter-account", Kind: Spend, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/key`", Shows: "the key's spend against its limit, or this month's spend without one"},
		},
		Endpoints: []Endpoint{{Host: "api.trustedrouter.com", Billing: Reseller, Source: "https://trustedrouter.com"}},
		Opencode:  []OpencodeID{{ID: "trustedrouter", Endpoint: "trustedrouter"}},
		EnvVars:   []string{"TRUSTEDROUTER_API_KEY"},
		ModelsDev: []string{"trustedrouter"},
	}
}
