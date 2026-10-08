package providers

func providerCodebuff() Descriptor {
	return Descriptor{
		ID:          "codebuff",
		DisplayName: "Codebuff",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Codebuff
			// (CodebuffUsageFetcher.swift), docs/codebuff.md. The codebuff CLI's
			// session, which alone reads the weekly rate limit, is read only
			// once granted (`setup codebuff --use-app-login`, ADR 0013).
			{Name: "codebuff_account", Tag: "codebuff-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				AppLogins: []AppLoginItem{{App: "the codebuff CLI", Kind: AppLoginJSON,
					Paths:  []string{"~/.config/manicode/credentials.json"},
					Fields: []string{"default.authToken|authToken"}, Host: "www.codebuff.com"}},
				Endpoint: "`POST /api/v1/usage`", Shows: "credits used against the quota until it resets; with the CLI's sign-in, the weekly rate limit too"},
		},
		EnvVars: []string{"CODEBUFF_API_KEY"},
	}
}
