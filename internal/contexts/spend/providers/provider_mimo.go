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
				Shows:    "the balance (in US dollars) and the Token Plan's monthly credits used"},
		},
		Docs: Docs{Label: "Xiaomi MiMo"},
	}
}
