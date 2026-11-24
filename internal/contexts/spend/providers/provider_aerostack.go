package providers

func providerAerostack() Descriptor {
	return Descriptor{
		ID:          "aerostack",
		DisplayName: "Aerostack",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/aerostack.ts,
			// Providers/Aerostack, docs/aerostack.md (a26ca57b); Aerostack's
			// billing docs (docs.aerostack.dev/billing). Not verified against
			// a live account.
			{Name: "aerostack_account", Tag: "aerostack-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				EnvVars:   []string{"AEROSTACK_TOKEN"},
				KeyFormat: "the account JWT the billing API takes (not an Agent Endpoint key)",
				Endpoint:  "`GET /api/billing/usage`",
				Shows:     "this month's AI tokens against the plan's allowance (no reset time in the answer)"},
		},
	}
}
