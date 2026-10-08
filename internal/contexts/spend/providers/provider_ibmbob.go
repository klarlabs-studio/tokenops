package providers

func providerIBMBob() Descriptor {
	return Descriptor{
		ID:          "ibmbob",
		DisplayName: "IBM Bob",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/IBMBob
			// (IBMBobUsageFetcher.swift); CodexBar has no docs page for it.
			{Name: "ibmbob_account", Tag: "ibmbob-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /admin/v1/profile`, `GET /admin/v1/teams/{team}/users/{user}`",
				Shows:    "Bobcoins used this month against the team budgets"},
		},
		EnvVars: []string{"BOBSHELL_API_KEY"},
	}
}
