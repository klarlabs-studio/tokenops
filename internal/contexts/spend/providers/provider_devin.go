package providers

func providerDevin() Descriptor {
	return Descriptor{
		ID:          "devin",
		DisplayName: "Devin",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Devin
			// (DevinUsageFetcher.swift, DevinUsageSnapshot.swift),
			// docs/devin.md. Setup reads the session from app.devin.ai's
			// localStorage in a Chromium browser, as CodexBar does: the auth1
			// token and the organisation last used (never the daemon, ADR
			// 0013 §8); ORG:TOKEN is typed instead with --paste or when no
			// browser has it. CodexBar's older auth0 keys are not read.
			{Name: "devin_web", Tag: "devin-web", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "ORG:TOKEN (the organisation's slug, its org_ ID or its app.devin.ai URL, " +
					"and the bearer token app.devin.ai sends)",
				LocalStorage: &LocalStorage{Origins: []string{"https://app.devin.ai"},
					Keys: []string{"*auth1_session", "last-internal-org-for-external-org-v1-*"}},
				Endpoint: "`GET app.devin.ai/api/<org>/billing/quota/usage`",
				Shows:    "the daily and weekly quota used, and the extra-usage balance"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup devin` reads app.devin.ai's session from your browser's localStorage, or takes ORG:TOKEN: the organisation and the bearer token app.devin.ai sends (its Authorization header)"},
	}
}
