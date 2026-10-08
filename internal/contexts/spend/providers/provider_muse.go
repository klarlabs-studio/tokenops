package providers

func providerMuse() Descriptor {
	return Descriptor{
		ID:          "muse",
		DisplayName: "Muse Code",
		Sources: []Source{
			// Muse Code has no API key: its CLI's own sign-in is the only
			// credential, read only once granted (ADR 0013). The file wins
			// over the Keychain item, as in CodexBar; the Keychain item is
			// only ever read without a prompt.
			{Name: "muse_account", Tag: "muse-account", Kind: Subscription, Credential: AppLogin,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Muse, Resources/Plugins/muse.ts (docs/muse.md)",
				AppLogins: []AppLoginItem{
					{App: "the Muse Code CLI", Kind: AppLoginJSON,
						Paths: []string{"~/.config/muse/auth.json"}, PathEnv: "MUSE_AUTH_PATH",
						Fields: []string{"providers.meta.access_token"}, Host: "api.meta.ai"},
					{App: "the Muse Code CLI", Kind: AppLoginKeychain,
						Service: "ai.meta.dev.credentials", Account: "meta",
						Fields: []string{"access_token"}, Host: "api.meta.ai"},
				},
				Endpoint: "`POST /muse-code/key`", Shows: "the subscription's 5-hour and weekly windows"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup muse --use-app-login`, after signing in with the Muse Code CLI"},
	}
}
