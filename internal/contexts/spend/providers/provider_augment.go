package providers

func providerAugment() Descriptor {
	return Descriptor{
		ID:          "augment",
		DisplayName: "Augment",
		Sources: []Source{
			{Name: "augment_cli", Tag: "augment-cli", Kind: Subscription, Credential: CLI,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Augment/AuggieCLIProbe.swift (docs/augment.md)",
				Endpoint:  "`auggie account status`",
				Shows:     "credits used of the month's allowance"},
			{Name: "augment_web", Tag: "augment-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Augment/AugmentStatusProbe.swift (docs/augment.md)",
				Cookie:    &Cookie{Host: "app.augmentcode.com", Names: []string{"_session", "web_rpc_proxy_session"}},
				Endpoint:  "`GET app.augmentcode.com/api/credits`",
				Shows:     "the same credits, with the app.augmentcode.com session"},
		},
		Docs: Docs{Label: "Augment Code", Setup: "nothing with the signed-in Auggie CLI; otherwise `tokenops vendor-usage setup augment` reads the app.augmentcode.com session"},
	}
}
