package providers

func providerHyper() Descriptor {
	return Descriptor{
		ID:          "hyper",
		DisplayName: "Charm Hyper",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/hyper.ts
			// (Providers/Hyper), docs/hyper.md.
			{Name: "hyper_account", Tag: "hyper-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET hyper.charm.land/v1/credits`",
				Shows:    "Hypercredits left"},
			{Name: "hyper_web", Tag: "hyper-web", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "hyper.charm.land", AllForHost: true},
				Endpoint: "the same, with hyper.charm.land's session",
				Shows:    "Hypercredits left"},
		},
		EnvVars: []string{"HYPER_API_KEY"},
	}
}
