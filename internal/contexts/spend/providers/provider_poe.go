package providers

func providerPoe() Descriptor {
	return Descriptor{
		ID:          "poe",
		DisplayName: "Poe",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/poe.js, Providers/Poe) and
			// Poe's Usage API docs; not verified against a live account.
			{Name: "poe_account", Tag: "poe-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /usage/current_balance`, `GET /usage/points_history`", Shows: "the point balance left and the points spent over the last 30 days (points, not dollars)"},
		},
		Endpoints: []Endpoint{{Host: "api.poe.com", Billing: Reseller, Source: "https://creator.poe.com/docs/resources/usage-api"}},
		Opencode:  []OpencodeID{{ID: "poe", Endpoint: "poe"}},
		EnvVars:   []string{"POE_API_KEY"},
		ModelsDev: []string{"poe"},
	}
}
