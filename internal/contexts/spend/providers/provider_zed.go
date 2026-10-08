package providers

func providerZed() Descriptor {
	return Descriptor{
		ID:          "zed",
		DisplayName: "Zed",
		Sources: []Source{
			{Name: "zed_account", Tag: "zed-account", Kind: Subscription, Credential: AppKeychain,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference:      "CodexBar Sources/CodexBarCore/Providers/Zed/ZedStatusProbe.swift, Resources/Plugins/zed.js (docs/zed.md)",
				KeychainServer: "https://zed.dev",
				KeyFormat:      "USER_ID ACCESS_TOKEN (Zed's own sign-in, separated by a space)",
				Endpoint:       "`GET cloud.zed.dev/client/users/me`",
				Shows:          "edit predictions used of the plan's allowance this billing cycle"},
		},
		Docs: Docs{Setup: "opt-in: `tokenops vendor-usage setup zed` reads the Zed editor's own sign-in from the Keychain once (macOS asks first)"},
	}
}
