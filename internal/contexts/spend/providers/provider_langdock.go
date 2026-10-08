package providers

func providerLangdock() Descriptor {
	return Descriptor{
		ID:          "langdock",
		DisplayName: "Langdock",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/langdock.ts
			// (Providers/Langdock), docs/langdock.md. CodexBar reads one
			// Microsoft Edge profile; setup's --browser picks the browser.
			{Name: "langdock_web", Tag: "langdock-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "app.langdock.com", Names: []string{"auth_token"}},
				Endpoint: "`GET /api/trpc/usageSettings.getPersonalUsage` (app.langdock.com)",
				Shows:    "the included 5-hour session and weekly limits used"},
		},
	}
}
