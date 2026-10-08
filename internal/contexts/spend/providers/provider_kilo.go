package providers

func providerKilo() Descriptor {
	return Descriptor{
		ID:          "kilo",
		DisplayName: "Kilo",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Kilo (KiloUsageFetcher.swift),
			// docs/kilo.md. The kilo CLI's own sign-in is read only once
			// granted (`setup kilo --use-app-login`, ADR 0013).
			{Name: "kilo_account", Tag: "kilo-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				AppLogins: []AppLoginItem{{App: "the kilo CLI", Kind: AppLoginJSON,
					Paths: []string{"~/.local/share/kilo/auth.json"}, Fields: []string{"kilo.access"}, Host: "app.kilo.ai"}},
				Endpoint: "`GET /api/trpc/user.getCreditBlocks,kiloPass.getState`",
				Shows:    "Kilo Pass credits used this billing period, and prepaid credit left",
				Scope:    "a Kilo organisation ID, read instead of the personal account (sent as `X-KILOCODE-ORGANIZATIONID`)"},
		},
		Endpoints: []Endpoint{{Host: "api.kilo.ai", Path: "/api/gateway", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "kilo", Endpoint: "kilo"}},
		EnvVars:   []string{"KILO_API_KEY"},
		ModelsDev: []string{"kilo"},
	}
}
