package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerFireworks() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderFireworks,
		DisplayName: "Fireworks",
		Logo:        true,
		Sources: []Source{
			// On unless switched off, and reads only when a Fireworks key is
			// on the machine: like the hook ledger it is always on rather
			// than enabled, and a machine without Fireworks is not a stale
			// one.
			{Name: "fireworks", Tag: "fireworks-usage", Kind: Spend, Credential: APIKey,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/fireworks",
				Fixture: "internal/contexts/spend/vendorusage/fireworks/reading_test.go",
				Shows:   "the month's spend against the account's or the member's cap"},
		},
		Endpoints: []Endpoint{{Host: "api.fireworks.ai", Billing: OwnCredential,
			Source: "https://docs.fireworks.ai/nexus/firerouter: closed models run on your own Anthropic or OpenAI account"}},
		Opencode:  []OpencodeID{{ID: "fireworks-ai", Endpoint: "fireworks"}, {ID: "fireworks", Endpoint: "fireworks"}},
		EnvVars:   []string{"FIREWORKS_API_KEY"},
		ModelsDev: []string{"fireworks-ai"},
		Docs:      Docs{Setup: "nothing: FireConnect's key or FIREWORKS_API_KEY is used"},
	}
}
