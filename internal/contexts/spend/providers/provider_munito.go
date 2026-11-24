package providers

func providerMunito() Descriptor {
	return Descriptor{
		ID:          "munito",
		DisplayName: "Munito",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/munito.ts,
			// Providers/Munito, docs/munito.md (a26ca57b); Munito's Admin API
			// (docs.munito.ai/api/getSpendLimits). It needs an admin key with
			// "Read account data", never an inference key. Not verified
			// against a live account.
			{Name: "munito_admin", Tag: "munito-admin", Kind: Spend, Credential: AdminKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				EnvVars:   []string{"MUNITO_ADMIN_KEY"},
				KeyFormat: "an admin API key with the Read account data permission",
				Endpoint:  "`GET /admin/v1/limits`",
				Shows:     "the organisation's spend this month against its limit, and the day's against its own"},
		},
		Endpoints: []Endpoint{
			{Host: "api.munito.ai", Billing: Reseller, Source: "https://docs.munito.ai"},
			{Host: "api.eu.munito.ai", Billing: Reseller, Source: "https://docs.munito.ai"},
		},
		// Inference keys cannot read the Admin API: they are attributed
		// and priced, never sent to it.
		Opencode:  []OpencodeID{{ID: "munito", Endpoint: "munito-api"}, {ID: "munito-eu", Endpoint: "munito-api"}},
		ModelsDev: []string{"munito", "munito-eu"},
	}
}
