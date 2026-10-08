package providers

func providerElevenLabs() Descriptor {
	return Descriptor{
		ID:          "elevenlabs",
		DisplayName: "ElevenLabs",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/elevenlabs.ts,
			// Providers/ElevenLabs, docs/elevenlabs.md) and ElevenLabs' API
			// reference; not verified against a live account.
			{Name: "elevenlabs_account", Tag: "elevenlabs-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /v1/user/subscription`", Shows: "the subscription's credits used this period"},
		},
		Endpoints: []Endpoint{{Host: "api.elevenlabs.io", Billing: Direct, Source: "https://elevenlabs.io/docs/api-reference/user/subscription/get"}},
		EnvVars:   []string{"ELEVENLABS_API_KEY", "XI_API_KEY"},
	}
}
