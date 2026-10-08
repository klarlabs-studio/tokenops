package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerXAI() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderXAI,
		DisplayName: "xAI",
		CatalogOnly: "metered through the proxy only; neither a plan nor an account is read",
	}
}
