package providers

func providerSakana() Descriptor {
	return Descriptor{
		ID:          "sakana",
		DisplayName: "Sakana AI",
		Logo:        true,
		// Ported from CodexBar's Sakana plugin
		// (Sources/CodexBarCore/Resources/Plugins/sakana.js, docs/sakana.md).
		// CodexBar imports no browser cookies for it, and neither does this:
		// the Cookie header is pasted, or SAKANA_COOKIE set.
		Sources: []Source{
			{Name: "sakana_web", Tag: "sakana-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "console.sakana.ai", PasteOnly: true},
				Endpoint: "`GET /billing` and `/billing?tab=payAsYouGo` (server-rendered pages)",
				Shows:    "5-hour and weekly quota windows, and the pay-as-you-go credit balance"},
		},
		EnvVars: []string{"SAKANA_COOKIE"},
	}
}
