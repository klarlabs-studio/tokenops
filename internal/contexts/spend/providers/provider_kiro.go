package providers

func providerKiro() Descriptor {
	return Descriptor{
		ID:          "kiro",
		DisplayName: "Kiro",
		Logo:        true,
		Sources: []Source{
			{Name: "kiro_cli", Tag: "kiro-cli", Kind: Subscription, Credential: CLI,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Kiro/KiroStatusProbe.swift (docs/kiro.md)",
				Endpoint:  "`kiro-cli chat --no-interactive /usage`",
				Shows:     "monthly plan credits used, and bonus credits"},
		},
		Docs: Docs{Setup: "nothing: read with the operator's signed-in kiro-cli (`kiro-cli login`)"},
	}
}
