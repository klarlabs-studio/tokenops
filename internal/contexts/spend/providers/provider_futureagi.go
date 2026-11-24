package providers

func providerFutureAGI() Descriptor {
	return Descriptor{
		ID:          "futureagi",
		DisplayName: "Future AGI",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/futureagi.ts,
			// Providers/FutureAGI, docs/futureagi.md (a26ca57b); Future AGI's
			// production OpenAPI. Not verified against a live account.
			{Name: "futureagi_account", Tag: "futureagi-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "API_KEY:SECRET_KEY",
				Endpoint:  "`GET /usage/v2/usage-overview/`",
				Shows:     "AI credits and gateway requests against the billing period's free allowances"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup futureagi` with the API key and the secret key, as API_KEY:SECRET_KEY"},
	}
}
