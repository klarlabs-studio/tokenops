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
		// The classic resource host, the Azure AI Foundry host the v1 API
		// accepts as well (learn.microsoft.com/azure/foundry/openai/api-version-lifecycle),
		// and the AI Services host an Azure OpenAI resource key is called on.
		Endpoints: []Endpoint{
			{Host: "openai.azure.com", Billing: Direct,
				Source: "https://learn.microsoft.com/azure/ai-foundry/openai/reference"},
			{Host: "services.ai.azure.com", Billing: Direct,
				Source: "https://learn.microsoft.com/azure/foundry/openai/api-version-lifecycle"},
			{Host: "cognitiveservices.azure.com", Billing: Direct,
				Source: "https://learn.microsoft.com/azure/foundry/openai/api-version-lifecycle"},
		},
		Opencode:  []OpencodeID{{ID: "azure", Endpoint: "azure-openai"}},
		ModelsDev: []string{"azure"},
	}
}
