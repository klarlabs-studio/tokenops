package providers

func providerAntigravity() Descriptor {
	return Descriptor{
		ID:          "antigravity",
		DisplayName: "Antigravity",
		Logo:        true,
		Sources: []Source{
			{Name: "antigravity_local", Tag: "antigravity-local", Kind: Subscription, Credential: LocalFile,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Antigravity/AntigravityStatusProbe.swift, AntigravityQuotaSummaryParser.swift (docs/antigravity.md)",
				Endpoint:  "the running app's local language server (`RetrieveUserQuotaSummary` on 127.0.0.1)",
				Shows:     "5-hour and weekly quota for Gemini models and for Claude and GPT models, while the app runs"},
		},
		Docs: Docs{Setup: "nothing: read from the Antigravity app while it runs"},
	}
}
