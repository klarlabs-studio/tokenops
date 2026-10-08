package providers

func providerDeepgram() Descriptor {
	return Descriptor{
		ID:          "deepgram",
		DisplayName: "Deepgram",
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/deepgram.js,
			// Providers/Deepgram), whose project discovery and Token auth it
			// keeps, and Deepgram's Management API docs for the balances;
			// not verified against a live account.
			{Name: "deepgram_account", Tag: "deepgram-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /v1/projects/{id}/balances`", Shows: "prepaid USD balance left, across the key's projects"},
		},
		Endpoints: []Endpoint{{Host: "api.deepgram.com", Billing: Direct, Source: "https://developers.deepgram.com/reference/manage/billing/list"}},
		EnvVars:   []string{"DEEPGRAM_API_KEY"},
	}
}
