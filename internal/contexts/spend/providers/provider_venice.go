package providers

func providerVenice() Descriptor {
	return Descriptor{
		ID:          "venice",
		DisplayName: "Venice",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/venice.js,
			// Providers/Venice) and Venice's API docs; not verified against a
			// live account. CodexBar's web source (a Clerk session cookie that
			// expires after about 60 seconds) is not ported: the daemon could
			// not keep it fresh without a browser tab.
			{Name: "venice_account", Tag: "venice-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /api/v1/billing/balance`", Shows: "USD balance left, and the DIEM epoch allocation used when staking"},
		},
		Endpoints: []Endpoint{{Host: "api.venice.ai", Billing: Reseller, Source: "https://docs.venice.ai/api-reference/endpoint/billing/balance"}},
		Opencode:  []OpencodeID{{ID: "venice", Endpoint: "venice"}},
		EnvVars:   []string{"VENICE_API_KEY", "VENICE_KEY"},
		ModelsDev: []string{"venice"},
	}
}
