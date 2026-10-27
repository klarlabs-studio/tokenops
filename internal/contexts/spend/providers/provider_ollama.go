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
			// docs/ollama.md.
			{Name: "ollama_api", Tag: "ollama-api", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /api/balance` (ollama.com)",
				Shows:    "Monthly included allowance used, its reset, and purchased credit balance"},
			{Name: "ollama_web", Tag: "ollama-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "ollama.com", Names: []string{"__Secure-session", "session", "ollama_session",
					"__Host-ollama_session", "wos-session", "__Secure-next-auth.session-token", "next-auth.session-token"}},
				Endpoint: "`GET ollama.com/settings` (the page; there is no usage API)",
				Shows:    "Ollama Cloud's monthly usage (or the older session and weekly windows)"},
		},
		EnvVars: []string{"OLLAMA_API_KEY"},
		Docs:    Docs{Label: "Ollama Cloud"},
	}
}
