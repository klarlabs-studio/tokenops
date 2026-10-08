package providers

func providerGrok() Descriptor {
	return Descriptor{
		ID:          "grok",
		DisplayName: "Grok",
		Logo:        true,
		// Ported from CodexBar's Grok provider
		// (Sources/CodexBarCore/Providers/Grok/GrokCreditsProxyFetcher.swift,
		// GrokWebBillingFetcher.swift, docs/grok.md). Not read: the Grok
		// CLI's ~/.grok/auth.json (another application's sign-in, ADR 0011
		// §1.4: its token is taken from GROK_OAUTH_TOKEN or setup instead),
		// and `grok agent stdio`'s x.ai/billing, which the current CLI
		// answers "method not found" outside its own TUI.
		Sources: []Source{
			{Name: "grok_account", Tag: "grok-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "the Grok CLI's sign-in token (GROK_OAUTH_TOKEN)",
				Endpoint:  "`GET cli-chat-proxy.grok.com/v1/billing?format=credits`",
				Shows:     "the SuperGrok credit window used, and the prepaid balance"},
			{Name: "grok_web", Tag: "grok-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "grok.com", AllForHost: true, Names: []string{"sso", "sso-rw"},
					Proof: []string{"sso", "sso-rw"}},
				Endpoint: "`POST grok.com/grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig` (gRPC-web)",
				Shows:    "the credit window used"},
		},
		EnvVars: []string{"GROK_OAUTH_TOKEN"},
		Docs:    Docs{Label: "Grok (SuperGrok)"},
	}
}
