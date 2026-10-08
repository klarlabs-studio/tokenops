package providers

func providerAbacus() Descriptor {
	return Descriptor{
		ID:          "abacus",
		DisplayName: "Abacus AI",
		// Ported from CodexBar's Abacus plugin
		// (Sources/CodexBarCore/Resources/Plugins/abacus.ts) and cookie
		// importer (Providers/Abacus/AbacusCookieImporter.swift: the session
		// cookie names it recognises; it sends every apps.abacus.ai cookie).
		Sources: []Source{
			{Name: "abacus_web", Tag: "abacus-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "apps.abacus.ai", AllForHost: true,
					Proof: []string{"sessionid", "session_id", "session_token", "auth_token", "access_token"}},
				Endpoint: "`GET /api/_getOrganizationComputePoints`, `POST /api/_getBillingInfo`",
				Shows:    "compute credits used this billing month, resetting at the next billing date"},
		},
		Docs: Docs{Label: "Abacus AI (ChatLLM)"},
	}
}
