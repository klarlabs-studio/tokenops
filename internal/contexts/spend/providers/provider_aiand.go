package providers

func providerAiAnd() Descriptor {
	return Descriptor{
		ID:          "aiand",
		DisplayName: "ai&",
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/aiand.ts, Providers/AiAnd,
			// docs/aiand.md) and ai&'s Request Logs docs; not verified against
			// a live account.
			{Name: "aiand_account", Tag: "aiand-account", Kind: Spend, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /logs?range=30days`", Shows: "the organisation's USD spend in the last 30 days, summed from its request logs"},
		},
		Endpoints: []Endpoint{{Host: "api.aiand.com", Billing: Reseller, Source: "https://docs.aiand.com/analytics/logs/"}},
		Opencode:  []OpencodeID{{ID: "aiand", Endpoint: "aiand"}},
		EnvVars:   []string{"AIAND_API_KEY"},
		ModelsDev: []string{"aiand"},
	}
}
