package providers

func providerNous() Descriptor {
	return Descriptor{
		ID:          "nous",
		DisplayName: "Nous Portal",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Nous,
			// Resources/Plugins/nous.ts, docs/nous.md. The token is a Portal
			// access token from Hermes Agent's sign-in, valid about an hour.
			// Hermes' own token file (~/.hermes/auth.json) is another
			// application's credential (ADR 0011 §1.4) and is not read.
			{Name: "nous_account", Tag: "nous-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /api/oauth/account`",
				Shows:    "the monthly credit grant used this period and the top-up credit left"},
		},
		EnvVars: []string{"NOUS_PORTAL_ACCESS_TOKEN"},
		Docs:    Docs{Label: "Nous Portal (Hermes Agent)"},
	}
}
