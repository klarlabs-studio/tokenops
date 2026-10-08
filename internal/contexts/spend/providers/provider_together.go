package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerTogether() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderTogether,
		DisplayName: "Together AI",
		CatalogOnly: "billed per token; its endpoint and models.dev prices are known, its account is not read",
		Endpoints:   []Endpoint{{Host: "api.together.xyz", Billing: Reseller}},
		Opencode:    []OpencodeID{{ID: "togetherai", Endpoint: "together"}, {ID: "together", Endpoint: "together"}},
		ModelsDev:   []string{"togetherai"},
	}
}
