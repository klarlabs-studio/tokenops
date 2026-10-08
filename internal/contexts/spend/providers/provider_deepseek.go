package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerDeepSeek() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderDeepSeek,
		DisplayName: "DeepSeek",
		Logo:        true,
		Sources: []Source{
			{Name: "deepseek_account", Tag: "deepseek-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /user/balance`", Shows: "prepaid USD balance left"},
		},
		Endpoints: []Endpoint{{Host: "api.deepseek.com", Billing: Direct, Source: "https://api-docs.deepseek.com/guides/anthropic_api"}},
		Opencode:  []OpencodeID{{ID: "deepseek", Endpoint: "deepseek"}},
		EnvVars:   []string{"DEEPSEEK_API_KEY"},
	}
}
