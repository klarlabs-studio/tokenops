package providers

func providerKimi() Descriptor {
	return Descriptor{
		ID:          "kimi",
		DisplayName: "Kimi",
		Logo:        true,
		Sources: []Source{
			{Name: "kimi_account", Tag: "kimi-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromClientSource,
				Endpoint: "`GET /coding/v1/usages`", Shows: "5-hour, weekly and monthly windows"},
		},
		Endpoints: []Endpoint{
			{Host: "api.kimi.com", Path: "/coding", Billing: Direct, Source: "https://www.kimi.com/code/docs/en/kimi-code/models"},
			{Host: "api.kimi.ai", Path: "/coding", Billing: Direct},
		},
		Opencode: []OpencodeID{
			{ID: "kimi-for-coding", Endpoint: "kimi"},
			{ID: "kimi-code-plan-global", Endpoint: "kimi"},
			{ID: "kimi-code-plan-cn", Endpoint: "kimi"},
		},
		// Kimi Code's turns are valued at Moonshot's pay-as-you-go rate.
		ModelsDev: []string{"moonshotai"},
		Docs:      Docs{Label: "Kimi Code"},
	}
}
