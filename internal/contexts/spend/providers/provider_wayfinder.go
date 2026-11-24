package providers

func providerWayfinder() Descriptor {
	return Descriptor{
		ID:          "wayfinder",
		DisplayName: "Wayfinder",
		// CodexBar's Wayfinder provider (Sources/CodexBarCore/Providers/
		// Wayfinder, docs/wayfinder.md, a26ca57b) polls a self-hosted
		// router's health, routing split and "savings".
		CatalogOnly: "a self-hosted router (wayfinder-router): the providers it routes to bill its turns, and its savings are its own estimate from the prices in its config, not a bill, so there is no account to read",
	}
}
