package providers

func providerDevin() Descriptor {
	return Descriptor{
		ID:          "devin",
		DisplayName: "Devin",
		Sources: []Source{
			// app.devin.ai keeps its session in localStorage, not a cookie:
			// setup reads the auth1 token and the organisation last used from
			// a Chromium browser (never the daemon), or the operator pastes
			// them. CodexBar's older auth0 session keys are not read.
			{Name: "devin_web", Tag: "devin-web", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Devin (DevinSessionImporter.swift, DevinUsageFetcher.swift, docs/devin.md)",
				KeyFormat: "AUTH1_TOKEN:ORGANIZATION (the internal org-... ID, or the slug in app.devin.ai/org/<slug>)",
				LocalStorage: &LocalStorage{Origins: []string{"https://app.devin.ai"},
					Keys: []string{"*auth1_session", "last-internal-org-for-external-org-v1-*"}},
				Endpoint: "`GET /api/<org>/billing/quota/usage`",
				Shows:    "daily and weekly quota, and the overage balance"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup devin` reads app.devin.ai's session from your browser's localStorage (or `--paste`)"},
	}
}
