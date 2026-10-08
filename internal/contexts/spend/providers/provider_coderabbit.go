package providers

func providerCodeRabbit() Descriptor {
	return Descriptor{
		ID:          "coderabbit",
		DisplayName: "CodeRabbit",
		Sources: []Source{
			{Name: "coderabbit_cli", Tag: "coderabbit-cli", Kind: Subscription, Credential: CLI,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/CodeRabbit/CodeRabbitCLIProbe.swift, CodeRabbitUsageParser.swift (docs/coderabbit.md)",
				Endpoint:  "`coderabbit usage`",
				Shows:     "reviews this billing period, a count with no allowance (no percentage)"},
		},
		Docs: Docs{Setup: "nothing: read with the operator's signed-in CodeRabbit CLI (`coderabbit auth login`)"},
	}
}
