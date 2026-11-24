package providers

func providerFal() Descriptor {
	return Descriptor{
		ID:          "fal",
		DisplayName: "fal",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/fal.js,
			// Providers/Fal, docs/fal.md (a26ca57b); fal's billing endpoint
			// (fal.ai/docs/platform-apis/v1/account/billing). Not verified
			// against a live account.
			{Name: "fal_account", Tag: "fal-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "a key with billing:usage:read (the BILLING, READONLY or FULL preset)",
				Endpoint:  "`GET /v1/account/billing?expand=credits`", Shows: "the account's credit balance"},
		},
		Endpoints: []Endpoint{
			{Host: "fal.run", Billing: Direct, Source: "https://fal.ai/docs"},
			{Host: "queue.fal.run", Billing: Direct, Source: "https://fal.ai/docs"},
		},
		EnvVars: []string{"FAL_KEY"},
	}
}
