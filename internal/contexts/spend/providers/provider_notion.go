package providers

func providerNotion() Descriptor {
	return Descriptor{
		ID:          "notion",
		DisplayName: "Notion AI",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/notion.ts,
			// Providers/Notion, docs/notion.md. Notion publishes no API for
			// the AI allowance; its web app's internal /api/v3 calls are read
			// with the token_v2 session cookie.
			{Name: "notion_account", Tag: "notion-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "notion.com", Names: []string{"token_v2"}},
				Endpoint: "`POST /api/v3/getCreditRateLimitStatus`",
				Shows:    "the Notion AI allowance used, rolling and this billing period (Business and Enterprise workspaces)"},
		},
	}
}
