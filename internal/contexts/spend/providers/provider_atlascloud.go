package providers

func providerAtlasCloud() Descriptor {
	return Descriptor{
		ID:          "atlascloud",
		DisplayName: "Atlas Cloud",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/atlascloud.js,
			// Providers/AtlasCloud) and Atlas Cloud's public API docs; not
			// verified against a live account.
			{Name: "atlascloud_account", Tag: "atlascloud-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /public/v1/balance`", Shows: "the account's available USD balance"},
		},
		Endpoints: []Endpoint{{Host: "api.atlascloud.ai", Billing: Reseller, Source: "https://www.atlascloud.ai/docs/public-api"}},
		EnvVars:   []string{"ATLASCLOUD_API_KEY"},
	}
}
