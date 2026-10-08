package providers

func providerFactory() Descriptor {
	return Descriptor{
		ID:          "factory",
		DisplayName: "Factory",
		Sources: []Source{
			// The Droid CLI keeps its API key in ~/.factory/.env, read only
			// once granted (ADR 0013). CodexBar's web path (WorkOS tokens
			// minted from a browser's localStorage) is not ported: it mints
			// tokens, which this source never does.
			{Name: "factory_account", Tag: "factory-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Factory (FactoryStatusProbe.swift, docs/factory.md)",
				AppLogins: []AppLoginItem{{App: "the Droid CLI", Kind: AppLoginEnvFile,
					Paths: []string{"~/.factory/.env"}, Fields: []string{"FACTORY_API_KEY"}, Host: "api.factory.ai"}},
				Endpoint: "`GET /api/billing/limits`", Shows: "the standard pool's 5-hour, weekly and monthly windows, and the extra-usage balance"},
		},
		EnvVars: []string{"FACTORY_API_KEY"},
		Docs:    Docs{Label: "Factory (Droid)"},
	}
}
