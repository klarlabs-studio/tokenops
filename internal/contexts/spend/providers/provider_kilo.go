package providers

func providerKilo() Descriptor {
	return Descriptor{
		ID:          "kilo",
		DisplayName: "Kilo",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Kilo (KiloUsageFetcher.swift),
			// docs/kilo.md. The kilo CLI's sign-in (~/.local/share/kilo/auth.json)
			// is another application's credential and is not read.
			{Name: "kilo_account", Tag: "kilo-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /api/trpc/user.getCreditBlocks,kiloPass.getState`",
				Shows:    "Kilo Pass credits used this billing period, and prepaid credit left"},
		},
		Endpoints: []Endpoint{{Host: "api.kilo.ai", Path: "/api/gateway", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "kilo", Endpoint: "kilo"}},
		EnvVars:   []string{"KILO_API_KEY"},
		ModelsDev: []string{"kilo"},
	}
}
