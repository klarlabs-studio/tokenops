package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerOllama() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderOllama,
		DisplayName: "Ollama",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Ollama
			// (OllamaUsageFetcher.swift, OllamaUsageParser.swift),
			// docs/ollama.md. CodexBar's API-key source only checks that
			// a key is accepted and reads no figure, so it is not a source
			// here.
			{Name: "ollama_web", Tag: "ollama-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "ollama.com", Names: []string{"__Secure-session", "session", "ollama_session",
					"__Host-ollama_session", "wos-session", "__Secure-next-auth.session-token", "next-auth.session-token"}},
				Endpoint: "`GET ollama.com/settings` (the page; there is no usage API)",
				Shows:    "Ollama Cloud's monthly usage (or the older session and weekly windows)"},
		},
		Docs: Docs{Label: "Ollama Cloud"},
	}
}
