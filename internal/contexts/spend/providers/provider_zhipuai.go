package providers

func providerZhipuAI() Descriptor {
	return Descriptor{
		ID:          "zhipuai",
		DisplayName: "Zhipu AI",
		// z.ai's mainland-China platform serves the GLM Coding Plan's quota
		// endpoint as z.ai does: same request, same answer (zai-org/ZCode
		// docs/bigmodel-coding-plan-usage-quota.md).
		Sources: []Source{
			{Name: "zhipuai_account", Tag: "zhipuai-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromClientSource,
				Endpoint: "`GET /api/monitor/usage/quota/limit`", Shows: "5-hour and weekly coding-plan windows"},
		},
		Endpoints: []Endpoint{
			{Host: "open.bigmodel.cn", Path: "/api/anthropic", Billing: Direct},
			{Host: "open.bigmodel.cn", Path: "/api/coding", Billing: Direct},
			{Host: "open.bigmodel.cn", Billing: Direct, Name: "zhipuai-api"},
		},
		Opencode:  []OpencodeID{{ID: "zhipuai-coding-plan", Endpoint: "zhipuai"}, {ID: "zhipuai", Endpoint: "zhipuai-api"}},
		ModelsDev: []string{"zhipuai"},
	}
}
