package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerOpenRouter() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderOpenRouter,
		DisplayName: "OpenRouter",
		Logo:        true,
		Sources: []Source{
			{Name: "openrouter_account", Tag: "openrouter-account", Kind: Spend, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /api/v1/key`", Shows: "the key's spend, against its credit cap when it has one"},
		},
		Endpoints: []Endpoint{{Host: "openrouter.ai", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "openrouter", Endpoint: "openrouter"}},
		EnvVars:   []string{"OPENROUTER_API_KEY"},
		ModelsDev: []string{"openrouter"},
	}
}
