package providers

func providerDevin() Descriptor {
	return Descriptor{
		ID:          "devin",
		DisplayName: "Devin",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Devin
			// (DevinUsageFetcher.swift, DevinUsageSnapshot.swift),
			// docs/devin.md. CodexBar also finds the token in Chromium's
			// localStorage for app.devin.ai; TokenOps reads no browser
			// localStorage, so the token is given in setup.
			{Name: "devin_web", Tag: "devin-web", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "ORG:TOKEN (the organisation's slug, its org_ ID or its app.devin.ai URL, " +
					"and the bearer token app.devin.ai sends)",
				Endpoint: "`GET app.devin.ai/api/<org>/billing/quota/usage`",
				Shows:    "the daily and weekly quota used, and the extra-usage balance"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup devin` with ORG:TOKEN: the organisation and the bearer token app.devin.ai sends (its Authorization header)"},
	}
}
