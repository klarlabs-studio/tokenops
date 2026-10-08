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
			// Hermes' own token file is read only once granted (`setup nous
			// --use-app-login`, ADR 0013), afresh on each poll, so the token
			// Hermes renews is picked up; it is never refreshed here.
			// CodexBar's credential_pool shape is not read.
			{Name: "nous_account", Tag: "nous-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				AppLogins: []AppLoginItem{{App: "Hermes Agent", Kind: AppLoginJSON,
					Paths:  []string{"~/.hermes/auth.json", "~/.hermes/shared/nous_auth.json"},
					Fields: []string{"providers.nous.access_token|access_token"}, Host: "portal.nousresearch.com"}},
				Endpoint: "`GET /api/oauth/account`",
				Shows:    "the monthly credit grant used this period and the top-up credit left"},
		},
		EnvVars: []string{"NOUS_PORTAL_ACCESS_TOKEN"},
		Docs:    Docs{Label: "Nous Portal (Hermes Agent)"},
	}
}
