package providers

func providerQwenCloud() Descriptor {
	return Descriptor{
		ID:          "qwencloud",
		DisplayName: "Qwen Cloud",
		Logo:        true,
		// Ported from CodexBar's Qwen Cloud provider
		// (Sources/CodexBarCore/Providers/QwenCloud/QwenCloudUsageFetcher.swift,
		// docs/qwen-cloud.md). Its session cookies are those the browser
		// sends to the console, proven by one of its sign-in tickets.
		Sources: []Source{
			{Name: "qwencloud_web", Tag: "qwencloud-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "home.qwencloud.com", AllForHost: true,
					Proof: []string{"login_aliyunid_ticket", "login_qwencloud_ticket", "qwen_sso_ticket"}},
				Endpoint: "`POST /data/api.json` on cs-data.qwencloud.com (tokenplan/personal/api/v2/usage)",
				Shows:    "the Individual Token Plan's 5-hour, weekly and monthly windows"},
		},
		EnvVars: []string{"QWEN_CLOUD_COOKIE"},
		Docs:    Docs{Label: "Qwen Cloud Token Plan"},
	}
}
