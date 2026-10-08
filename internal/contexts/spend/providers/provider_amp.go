package providers

func providerAmp() Descriptor {
	return Descriptor{
		ID:          "amp",
		DisplayName: "Amp",
		Logo:        true,
		Sources: []Source{
			{Name: "amp_cli", Tag: "amp-cli", Kind: Subscription, Credential: CLI,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Amp/AmpCLIProbe.swift, AmpUsageParser.swift (docs/amp.md)",
				Endpoint:  "`amp usage`",
				Shows:     "subscription agent usage and orb hours, Amp Free's daily allowance, individual credits"},
			{Name: "amp_account", Tag: "amp-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Amp/AmpUsageFetcher.swift (docs/amp.md)",
				Prompt:    "an Amp access token (ampcode.com settings)",
				Endpoint:  "`POST ampcode.com/api/internal?userDisplayBalanceInfo`",
				Shows:     "the same figures, with an access token"},
		},
		// No opencode ID: the token goes only to Amp's own reader.
		EnvVars: []string{"AMP_API_KEY"},
		Docs:    Docs{Setup: "nothing with the signed-in Amp CLI; otherwise AMP_API_KEY or `tokenops vendor-usage setup amp` with an access token"},
	}
}
