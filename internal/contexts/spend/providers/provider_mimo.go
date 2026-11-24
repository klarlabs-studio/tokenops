package providers

func providerMiMo() Descriptor {
	return Descriptor{
		ID:          "mimo",
		DisplayName: "Xiaomi MiMo",
		Logo:        true,
		// Ported from CodexBar's MiMo provider
		// (Sources/CodexBarCore/Providers/MiMo/MiMoUsageFetcher.swift,
		// MiMoCookieImporter.swift: the two cookies a session needs and the
		// two it may carry). Its local-wrapper fallback is not ported.
		Sources: []Source{
			{Name: "mimo_web", Tag: "mimo-web", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "platform.xiaomimimo.com",
					Names: []string{"api-platform_serviceToken", "userId", "api-platform_ph", "api-platform_slh"}},
				Endpoint: "`GET /api/v1/balance`, `/api/v1/tokenPlan/detail`, `/api/v1/tokenPlan/usage`",
				Shows:    "the balance (in its currency) and the Token Plan's monthly credits used"},
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/mimo.ts
			// (94cc400e1b, a26ca57b). The Token Plan with its own API key
			// (tp-…), in the cn region unless the key is given as sgp:KEY.
			// Not verified against a live account.
			{Name: "mimo_token_plan", Tag: "mimo-token-plan", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Resources/Plugins/mimo.ts",
				KeyFormat: "a Token Plan API key (tp-…), as sgp:KEY for the Singapore region",
				Endpoint:  "`GET /v1/platform/token_plan` (token-plan-cn or token-plan-sgp.xiaomimimo.com)",
				Shows:     "the Token Plan's credits used over its period"},
		},
		EnvVars: []string{"MIMO_API_KEY"},
		Docs:    Docs{Label: "Xiaomi MiMo"},
	}
}
