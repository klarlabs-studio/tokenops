package providers

func providerMuse() Descriptor {
	return Descriptor{
		ID:          "muse",
		DisplayName: "Muse Code",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Muse
			// (MuseCredentials.swift) and Resources/Plugins/muse.ts,
			// docs/muse.md. The token is given in setup, or, once granted
			// (`setup muse --use-app-login`, ADR 0013), read from Muse Code's
			// own ~/.config/muse/auth.json or, without a prompt, its Keychain
			// item. The dev.meta.ai browser-team fallback is not read.
			{Name: "muse_account", Tag: "muse-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				AppLogins: []AppLoginItem{
					{App: "the Muse Code CLI", Kind: AppLoginJSON,
						Paths: []string{"~/.config/muse/auth.json"}, PathEnv: "MUSE_AUTH_PATH",
						Fields: []string{"providers.meta.access_token"}, Host: "api.meta.ai"},
					{App: "the Muse Code CLI", Kind: AppLoginKeychain,
						Service: "ai.meta.dev.credentials", Account: "meta",
						Fields: []string{"access_token"}, Host: "api.meta.ai"},
				},
				KeyFormat: "the dca: device token `muse login` stored (providers.meta.access_token in ~/.config/muse/auth.json)",
				Endpoint:  "`POST api.meta.ai/muse-code/key`",
				Shows:     "the 5-hour and weekly quota used"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup muse` with the dca: device token from `muse login`"},
	}
}
