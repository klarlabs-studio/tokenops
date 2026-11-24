package providers

func providerTavily() Descriptor {
	return Descriptor{
		ID:          "tavily",
		DisplayName: "Tavily",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/tavily.js,
			// Providers/Tavily, docs/tavily.md (a26ca57b); Tavily's Usage API
			// (docs.tavily.com/documentation/api-reference/endpoint/usage).
			// Not verified against a live account.
			{Name: "tavily_account", Tag: "tavily-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /usage`", Shows: "the plan's monthly API credits used (no reset time: Tavily does not give its time zone)"},
		},
		Endpoints: []Endpoint{{Host: "api.tavily.com", Billing: Direct, Source: "https://docs.tavily.com/documentation/api-credits"}},
		EnvVars:   []string{"TAVILY_API_KEY"},
	}
}
