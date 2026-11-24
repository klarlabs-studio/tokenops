package providers

func providerCosmic() Descriptor {
	return Descriptor{
		ID:          "cosmic",
		DisplayName: "Cosmic AI",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/cosmic.ts,
			// Providers/Cosmic, docs/cosmic.md (a26ca57b); the route under the
			// official CLI's `cosmic billing usage` (github.com/cosmicjs/cli).
			// Not verified against a live account.
			{Name: "cosmic_project", Tag: "cosmic-project", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "PROJECT_ID:PERSONAL_ACCESS_TOKEN",
				Endpoint:  "`GET /v3/projects/usage` (dapi.cosmicjs.com)",
				Shows:     "a project's AI input and output tokens against the plan's allowances (no reset time in the answer)"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup cosmic` with the project ID and a Personal Access Token (Account Settings → API Tokens), as PROJECT_ID:PERSONAL_ACCESS_TOKEN"},
	}
}
