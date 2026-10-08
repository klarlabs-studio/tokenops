package providers

func providerAntigravity() Descriptor {
	return Descriptor{
		ID:          "antigravity",
		DisplayName: "Antigravity",
		Logo:        true,
		Sources: []Source{
			// The language server's CSRF token is the app's own credential,
			// on its command line: read only once granted (ADR 0013), and
			// sent nowhere but that server on 127.0.0.1.
			{Name: "antigravity_local", Tag: "antigravity-local", Kind: Subscription, Credential: AppLogin,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				AppLogins: []AppLoginItem{{App: "Antigravity", Kind: AppLoginProcess,
					Process: "language_server*|language-server*", Markers: []string{"antigravity", "/gemini.app/"},
					Fields: []string{"--csrf_token", "pid"}, Host: "127.0.0.1"}},
				Reference: "CodexBar Sources/CodexBarCore/Providers/Antigravity/AntigravityStatusProbe.swift, AntigravityQuotaSummaryParser.swift (docs/antigravity.md)",
				Endpoint:  "the running app's local language server (`RetrieveUserQuotaSummary` on 127.0.0.1)",
				Shows:     "5-hour and weekly quota for Gemini models and for Claude and GPT models, while the app runs"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup antigravity --use-app-login` while the app runs; then read while it runs"},
	}
}
