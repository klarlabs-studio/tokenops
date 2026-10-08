package providers

import "time"

func providerAlibaba() Descriptor {
	return Descriptor{
		ID:          "alibaba",
		DisplayName: "Alibaba Cloud",
		CatalogOnly: "the Coding Plan's endpoint, prices and limits are known; nothing reads its usage yet",
		Endpoints: []Endpoint{{Host: "coding-intl.dashscope.aliyuncs.com", Billing: Reseller,
			Source: "https://www.alibabacloud.com/help/en/model-studio/coding-plan"}},
		Opencode: []OpencodeID{
			{ID: "alibaba-coding-plan", Endpoint: "alibaba"},
			{ID: "alibaba-coding-plan-cn", Endpoint: "alibaba"},
			{ID: "alibaba", Endpoint: "alibaba-api"},
			{ID: "alibaba-cn", Endpoint: "alibaba-api"},
		},
		ModelsDev: []string{"alibaba"},
		Docs:      Docs{CatalogLabel: "Alibaba Coding"},
		Plans: []Plan{
			{
				Name: "alibaba-coding-pro", Display: "Alibaba Cloud Coding Plan Pro",
				RateLimitWindow: 5 * time.Hour, MessagesPerWindow: 6000, WindowUnit: "requests",
				SourceURL:   "https://www.alibabacloud.com/help/en/model-studio/coding-plan (2026-10-01): 6,000 requests per 5 hours, 45,000 per week, 90,000 per month",
				MonthlyUSD:  50,
				PriceSource: "https://www.alibabacloud.com/help/en/model-studio/coding-plan (2026-10-01): Pro $50 per month",
			},
		},
	}
}
