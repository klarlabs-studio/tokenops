package providers

func providerWorkBuddy() Descriptor {
	return Descriptor{
		ID:          "workbuddy",
		DisplayName: "WorkBuddy",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/workbuddy.ts,
			// Providers/WorkBuddy (WorkBuddyChromeVersion.swift),
			// docs/workbuddy.md. The session cookies' names are not
			// published, so every www.workbuddy.cn cookie is read. The
			// desktop app's encrypted token in ~/.workbuddy is not read.
			{Name: "workbuddy_web", Tag: "workbuddy-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "www.workbuddy.cn", AllForHost: true},
				Endpoint: "`POST /billing/meter/get-user-resource-summary` (www.workbuddy.cn)",
				Shows:    "the share of the credit packages' cycle used, and when it ends"},
		},
		Docs: Docs{Label: "WorkBuddy (Tencent)"},
	}
}
