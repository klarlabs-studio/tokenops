package providers

func providerLinkup() Descriptor {
	return Descriptor{
		ID:          "linkup",
		DisplayName: "Linkup",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/linkup.js,
			// Providers/Linkup, docs/linkup.md (a26ca57b); Linkup's balance
			// endpoint (docs.linkup.so). Not verified against a live account.
			{Name: "linkup_account", Tag: "linkup-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/credits/balance`", Shows: "the prepaid USD credit left"},
		},
		Endpoints: []Endpoint{{Host: "api.linkup.so", Billing: Direct, Source: "https://docs.linkup.so/pages/documentation/endpoints/account/balance"}},
		EnvVars:   []string{"LINKUP_API_KEY"},
	}
}
