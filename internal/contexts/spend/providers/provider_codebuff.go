package providers

func providerCodebuff() Descriptor {
	return Descriptor{
		ID:          "codebuff",
		DisplayName: "Codebuff",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Codebuff
			// (CodebuffUsageFetcher.swift), docs/codebuff.md. The codebuff CLI's
			// session (~/.config/manicode/credentials.json), which alone reads
			// the weekly rate limit, is another application's sign-in and is
			// not read.
			{Name: "codebuff_account", Tag: "codebuff-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`POST /api/v1/usage`", Shows: "credits used against the quota until it resets"},
		},
		EnvVars: []string{"CODEBUFF_API_KEY"},
	}
}
