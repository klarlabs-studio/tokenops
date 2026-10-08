package providers

func providerZhipuAI() Descriptor {
	return Descriptor{
		ID:          "zhipuai",
		DisplayName: "Zhipu AI",
		CatalogOnly: "z.ai's mainland-China platform: its endpoints, opencode IDs and prices are known; no reader reads its plan yet",
		Endpoints: []Endpoint{
			{Host: "open.bigmodel.cn", Path: "/api/anthropic", Billing: Direct},
			{Host: "open.bigmodel.cn", Path: "/api/coding", Billing: Direct},
			{Host: "open.bigmodel.cn", Billing: Direct, Name: "zhipuai-api"},
		},
		Opencode:  []OpencodeID{{ID: "zhipuai-coding-plan", Endpoint: "zhipuai"}, {ID: "zhipuai", Endpoint: "zhipuai-api"}},
		ModelsDev: []string{"zhipuai"},
	}
}
