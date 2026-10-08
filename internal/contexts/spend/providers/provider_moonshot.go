package providers

func providerMoonshot() Descriptor {
	return Descriptor{
		ID:          "moonshot",
		DisplayName: "Moonshot",
		Logo:        true,
		Sources: []Source{
			{Name: "moonshot_account", Tag: "moonshot-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /v1/users/me/balance`", Shows: "prepaid USD balance left"},
		},
		// The Kimi Code membership is under api.kimi.com/coding (kimi); the
		// API is here.
		Endpoints: []Endpoint{{Host: "api.moonshot.ai", Billing: Direct, Source: "https://platform.kimi.ai/docs/guide/claude-code-kimi"}},
		Opencode:  []OpencodeID{{ID: "moonshotai", Endpoint: "moonshot"}, {ID: "moonshotai-cn", Endpoint: "moonshot"}},
		EnvVars:   []string{"MOONSHOT_API_KEY"},
		ModelsDev: []string{"moonshotai"},
		Docs:      Docs{Label: "Moonshot (Kimi API)"},
	}
}
