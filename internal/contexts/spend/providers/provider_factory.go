package providers

func providerFactory() Descriptor {
	return Descriptor{
		ID:          "factory",
		DisplayName: "Factory",
		// Ported from CodexBar's Factory (Droid) provider
		// (Sources/CodexBarCore/Providers/Factory/FactoryStatusProbe.swift,
		// FactoryStatusProbe+APIKey.swift, docs/factory.md). CodexBar's
		// other sources are not read: Droid's ~/.factory/.env and its
		// WorkOS tokens in browser localStorage belong to other
		// applications (ADR 0011 §1.4), and refreshing a WorkOS token
		// would sign the browser out.
		Sources: []Source{
			{Name: "factory_account", Tag: "factory-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /api/billing/limits`, `/api/organization/subscription/usage` (api.factory.ai)",
				Shows:    "5-hour, weekly and monthly windows (and the Core fallback's), or the Standard and Premium token allowances; the extra-usage balance"},
			{Name: "factory_web", Tag: "factory-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "app.factory.ai", Also: []string{"auth.factory.ai"},
					Names: []string{"wos-session", "access-token", "__Secure-next-auth.session-token", "next-auth.session-token",
						"__Secure-authjs.session-token", "authjs.session-token", "__Host-authjs.csrf-token", "session"},
					Proof: []string{"wos-session", "access-token", "__Secure-next-auth.session-token", "next-auth.session-token",
						"__Secure-authjs.session-token", "authjs.session-token", "session"}},
				Endpoint: "the same, with app.factory.ai's session",
				Shows:    "the same windows and balance"},
		},
		EnvVars: []string{"FACTORY_API_KEY"},
		Docs:    Docs{Label: "Factory (Droid)"},
	}
}
