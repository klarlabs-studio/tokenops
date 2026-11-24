package providers

func providerSailResearch() Descriptor {
	return Descriptor{
		ID:          "sailresearch",
		DisplayName: "Sail Research",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/sailresearch.ts,
			// Providers/SailResearch, docs/sailresearch.md (a26ca57b); Sail's
			// Usage API (docs.sailresearch.com/usage-endpoints). Not verified
			// against a live account.
			{Name: "sailresearch_account", Tag: "sailresearch-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v2/usage/summary?range=30d`",
				Shows:    "the organisation's credit balance and its spend over the last 30 days (7 on a free organisation)"},
		},
		Endpoints: []Endpoint{{Host: "api.sailresearch.com", Billing: Direct, Source: "https://docs.sailresearch.com/usage"}},
		EnvVars:   []string{"SAIL_API_KEY"},
	}
}
