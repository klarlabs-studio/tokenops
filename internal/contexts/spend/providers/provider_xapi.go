package providers

func providerXAPI() Descriptor {
	return Descriptor{
		ID:          "xapi",
		DisplayName: "X API",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/xapi.js
			// (Providers/XAPI), docs/xapi.md. An X API bearer token does
			// not read the console.
			{Name: "xapi_web", Tag: "xapi-web", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "console.x.com", Names: []string{"auth_token", "ct0"}, Proof: []string{"auth_token"}},
				Endpoint: "`GET /api/me`, `/api/accounts/{id}/credits` (console.x.com)",
				Shows:    "prepaid credit left, purchased plus free (below zero when overdrawn)"},
		},
		Docs: Docs{Label: "X API (console.x.com)"},
	}
}
