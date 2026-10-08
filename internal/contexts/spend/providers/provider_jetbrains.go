package providers

func providerJetBrains() Descriptor {
	return Descriptor{
		ID:          "jetbrains",
		DisplayName: "JetBrains AI",
		Sources: []Source{
			{Name: "jetbrains_local", Tag: "jetbrains-local", Kind: Subscription, Credential: LocalFile,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/JetBrains/JetBrainsStatusProbe.swift, JetBrainsQuotaLogReader.swift (docs/jetbrains.md)",
				Endpoint:  "the IDE's `options/AIAssistantQuotaManager2.xml` and `idea.log`",
				Shows:     "monthly AI credits used, from the most recently used IDE"},
		},
		Docs: Docs{Setup: "nothing: read from the IDE's own files once AI Assistant has been used"},
	}
}
