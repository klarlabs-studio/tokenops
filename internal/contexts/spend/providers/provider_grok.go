package providers

func providerGrok() Descriptor {
	return Descriptor{
		ID:          "grok",
		DisplayName: "Grok",
		Sources: []Source{
			// A SuperGrok subscription's credits, read with the grok CLI's
			// own sign-in once granted (ADR 0013): the OIDC entry of
			// ~/.grok/auth.json, else the sign-in entry. Its refresh token is
			// never read; the CLI renews the access token. xAI's API
			// balance is the xai provider.
			{Name: "grok_account", Tag: "grok-account", Kind: Subscription, Credential: AppLogin,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Grok (GrokCreditsProxyFetcher.swift, GrokAuth.swift, docs/grok.md)",
				AppLogins: []AppLoginItem{{App: "the grok CLI", Kind: AppLoginJSON,
					Paths:  []string{"~/.grok/auth.json"},
					Fields: []string{"{https://auth.x.ai::*}.key|{*/sign-in*}.key"}, Host: "cli-chat-proxy.grok.com"}},
				Endpoint: "`GET /v1/billing?format=credits`", Shows: "the billing period's credits used, and the prepaid balance"},
		},
		Docs: Docs{Label: "Grok (SuperGrok)", Setup: "`tokenops vendor-usage setup grok --use-app-login`, after signing in with the grok CLI"},
	}
}
