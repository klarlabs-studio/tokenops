package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerGroq() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderGroq,
		DisplayName: "Groq",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Groq
			// (GroqConsoleSession.swift, GroqConsoleStytch.swift,
			// GroqConsoleFetcher.swift), docs/groq.md. CodexBar's
			// Enterprise Prometheus source reports request and token rates
			// per minute, which no reading here holds; it is not read.
			{Name: "groq_web", Tag: "groq-web", Kind: Spend, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "console.groq.com", Names: []string{"stytch_session", "stytch_session_jwt"},
					Proof: []string{"stytch_session", "stytch_session_jwt"}},
				Endpoint: "`GET api.groq.com/platform/v1/organizations/{org}/activity`",
				Shows:    "the organisation's GroqCloud spend this UTC month"},
		},
		Docs: Docs{Label: "GroqCloud"},
	}
}
