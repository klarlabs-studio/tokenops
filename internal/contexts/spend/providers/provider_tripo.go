package providers

func providerTripo() Descriptor {
	return Descriptor{
		ID:          "tripo",
		DisplayName: "Tripo",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/tripo.ts,
			// Providers/Tripo, docs/tripo.md (a26ca57b); Tripo's account API
			// (developers.tripo3d.ai/en/docs/account). Not verified against a
			// live account.
			{Name: "tripo_account", Tag: "tripo-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v3/account/balance`", Shows: "the credits available, in credits"},
		},
		Endpoints: []Endpoint{{Host: "openapi.tripo3d.ai", Billing: Direct, Source: "https://developers.tripo3d.ai/en/docs/billing"}},
		EnvVars:   []string{"TRIPO_API_KEY"},
	}
}
