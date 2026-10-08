package providers

func providerMuse() Descriptor {
	return Descriptor{
		ID:          "muse",
		DisplayName: "Muse Code",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Muse
			// (MuseCredentials.swift) and Resources/Plugins/muse.ts,
			// docs/muse.md. CodexBar reads the token from Muse Code's
			// ~/.config/muse/auth.json or its Keychain item; that is
			// another application's credential (ADR 0011 §1.4), so here
			// the operator gives it in setup. The dev.meta.ai browser-team
			// fallback is not read.
			{Name: "muse_account", Tag: "muse-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "the dca: device token `muse login` stored (providers.meta.access_token in ~/.config/muse/auth.json)",
				Endpoint:  "`POST api.meta.ai/muse-code/key`",
				Shows:     "the 5-hour and weekly quota used"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup muse` with the dca: device token from `muse login`"},
	}
}
