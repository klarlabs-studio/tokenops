package providers

func providerQoder() Descriptor {
	return Descriptor{
		ID:          "qoder",
		DisplayName: "Qoder",
		Logo:        true,
		// Ported from CodexBar's Qoder plugin
		// (Sources/CodexBarCore/Resources/Plugins/qoder.js), which sends
		// every cookie of the site: its session cookie names are not known.
		Sources: []Source{
			{Name: "qoder_web", Tag: "qoder-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "qoder.com", Also: []string{"qoder.com.cn"}, AllForHost: true},
				Endpoint: "`GET /api/v2/me/usages/big_model_credits` (qoder.com or qoder.com.cn)",
				Shows:    "big-model credits used against the plan's (and the team's shared) total, until the next reset"},
		},
	}
}
