package providers

func providerSofya() Descriptor {
	return Descriptor{
		ID:          "sofya",
		DisplayName: "Sofya",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/sofya.ts,
			// Providers/Sofya, docs/sofya.md (a26ca57b); Sofya's account API
			// (sofya.co/docs). Not verified against a live account.
			{Name: "sofya_account", Tag: "sofya-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/auth/me`", Shows: "the account's available credits, in credits"},
		},
		EnvVars: []string{"SOFYA_API_KEY"},
	}
}
