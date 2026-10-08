package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerLiteLLM() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderLiteLLM,
		DisplayName: "LiteLLM",
		Sources: []Source{
			{Name: "litellm_gateway", Tag: "litellm-account", Kind: Gateway, Credential: APIKey,
				Switch: SwitchAccounts, Reader: GatewayReader, Verified: FromClientSource,
				RecognisedBy: "`GET /health/liveliness`",
				Endpoint:     "`GET /key/info`", Shows: "the key's spend against its budget, and when it resets"},
		},
		Docs: Docs{Label: "LiteLLM proxy"},
	}
}
