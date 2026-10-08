package providers

func providerAzureOpenAI() Descriptor {
	return Descriptor{
		ID:          "azure-openai",
		DisplayName: "Azure OpenAI",
		Logo:        true,
		// CodexBar's Azure OpenAI provider (Sources/CodexBarCore/Providers/
		// AzureOpenAI, docs/azure-openai.md) reads no usage: it sends a
		// billable chat completion to check that a deployment answers.
		CatalogOnly: "billed per token by Azure; a resource's key reads no spend, quota or usage (Azure Cost Management needs an Entra ID sign-in), so its turns are attributed and priced but its account is not read",
		Endpoints: []Endpoint{{Host: "openai.azure.com", Billing: Direct,
			Source: "https://learn.microsoft.com/azure/ai-foundry/openai/reference"}},
		Opencode:  []OpencodeID{{ID: "azure", Endpoint: "azure-openai"}},
		ModelsDev: []string{"azure"},
	}
}
