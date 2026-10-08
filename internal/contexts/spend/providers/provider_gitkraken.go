package providers

func providerGitKraken() Descriptor {
	return Descriptor{
		ID:          "gitkraken",
		DisplayName: "GitKraken AI",
		Sources: []Source{
			{Name: "gitkraken_account", Tag: "gitkraken-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Resources/Plugins/gitkraken (docs/gitkraken.md)",
				Endpoint:  "`GET api.gitkraken.dev/v1/ai-tasks/usage`",
				Shows:     "weekly AI credits, and the organization's pool with GITKRAKEN_ORG_ID"},
		},
		// No opencode ID: the key is the account session's bearer token,
		// copied from gitkraken.dev/account, and goes only to this reader.
		EnvVars: []string{"GITKRAKEN_API_TOKEN"},
		Docs:    Docs{Setup: "`tokenops vendor-usage setup gitkraken` takes the bearer token of gitkraken.dev/account's usage request, or set GITKRAKEN_API_TOKEN"},
	}
}
