package providers

func providerAbliteration() Descriptor {
	return Descriptor{
		ID:          "abliteration",
		DisplayName: "abliteration.ai",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/abliteration.ts,
			// Providers/Abliteration, docs/abliteration.md (a26ca57b). Not
			// verified against a live account.
			{Name: "abliteration_account", Tag: "abliteration-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/credits`", Shows: "the USD credit left (credits bought less used)"},
		},
		Endpoints: []Endpoint{{Host: "api.abliteration.ai", Billing: Direct, Source: "https://abliteration.ai"}},
		Opencode:  []OpencodeID{{ID: "abliteration-ai", Endpoint: "abliteration"}},
		EnvVars:   []string{"ABLITERATION_API_KEY", "ABLIT_KEY"},
		ModelsDev: []string{"abliteration-ai"},
	}
}
