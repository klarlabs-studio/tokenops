package providers

func providerCommandCode() Descriptor {
	return Descriptor{
		ID:          "commandcode",
		DisplayName: "Command Code",
		Logo:        true,
		// Ported from CodexBar's Command Code provider
		// (Sources/CodexBarCore/Providers/CommandCode/CommandCodeUsageFetcher.swift,
		// CommandCodeCookieHeader.swift: the better-auth session cookies).
		Sources: []Source{
			{Name: "commandcode_web", Tag: "commandcode-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "commandcode.ai", Also: []string{"www.commandcode.ai"},
					Names: []string{
						"__Secure-commandcode_prod_.session_token", "commandcode_prod_.session_token",
						"__Host-commandcode_prod_.session_token", "__Host-better-auth.session_token",
						"__Secure-better-auth.session_token", "better-auth.session_token",
					},
					Proof: []string{
						"__Secure-commandcode_prod_.session_token", "commandcode_prod_.session_token",
						"__Host-commandcode_prod_.session_token", "__Host-better-auth.session_token",
						"__Secure-better-auth.session_token", "better-auth.session_token",
					}},
				Endpoint: "`GET /internal/billing/credits`, `/internal/billing/subscriptions` (api.commandcode.ai)",
				Shows:    "5-hour and weekly limits, the monthly credit grant used, and purchased credits"},
		},
	}
}
