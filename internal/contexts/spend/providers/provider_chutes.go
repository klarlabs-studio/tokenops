package providers

func providerChutes() Descriptor {
	return Descriptor{
		ID:          "chutes",
		DisplayName: "Chutes",
		Sources: []Source{
			{Name: "chutes_account", Tag: "chutes-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromClientSource,
				Endpoint: "`GET /users/me/subscription_usage`", Shows: "the 4-hour and monthly caps"},
		},
		Endpoints: []Endpoint{{Host: "llm.chutes.ai", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "chutes", Endpoint: "chutes"}},
		EnvVars:   []string{"CHUTES_API_KEY"},
		ModelsDev: []string{"chutes"},
	}
}
