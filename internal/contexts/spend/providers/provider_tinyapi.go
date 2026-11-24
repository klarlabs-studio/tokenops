package providers

func providerTinyAPI() Descriptor {
	return Descriptor{
		ID:          "tinyapi",
		DisplayName: "TinyApi",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/tinyapi.js,
			// Providers/TinyApi, docs/tinyapi.md (a26ca57b). The dashboard's
			// credits endpoint, read with the browser session: TinyApi's
			// tool-execution keys cannot read it. Not verified against a live
			// account.
			{Name: "tinyapi_web", Tag: "tinyapi-web", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "tinyapi.rest", Names: []string{"__Secure-authjs.session-token*", "authjs.session-token*"},
					Proof: []string{"__Secure-authjs.session-token*", "authjs.session-token*"}},
				Endpoint: "`GET /api/user/credits` (tinyapi.rest)", Shows: "the credits available, in credits"},
		},
	}
}
