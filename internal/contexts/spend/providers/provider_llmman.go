package providers

func providerLLMMan() Descriptor {
	return Descriptor{
		ID:          "llmman",
		DisplayName: "llmman",
		// CodexBar's llmman provider (Sources/CodexBarCore/Resources/Plugins/
		// llmman.ts, docs/llmman.md, a26ca57b) reads the local daemon's
		// node report: model memory occupied by loaded models.
		CatalogOnly: "runs models locally (llmman serve): its node report is the memory loaded models occupy, not usage, spend or a plan, and the hosted providers it routes to bill their own turns",
	}
}
