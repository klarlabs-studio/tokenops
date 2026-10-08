package providers

import "time"

func providerZAI() Descriptor {
	return Descriptor{
		ID:          "zai",
		DisplayName: "z.ai",
		Logo:        true,
		Sources: []Source{
			{Name: "zai_account", Tag: "zai-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromClientSource,
				Endpoint: "`GET /api/monitor/usage/quota/limit`", Shows: "5-hour and weekly token windows"},
		},
		// The GLM Coding Plan is served under /api/anthropic and /api/coding;
		// the pay-as-you-go API under /api/paas.
		Endpoints: []Endpoint{
			{Host: "api.z.ai", Path: "/api/anthropic", Billing: Direct, Source: "https://docs.z.ai/devpack/quick-start"},
			{Host: "api.z.ai", Path: "/api/coding", Billing: Direct, Source: "https://docs.z.ai/devpack/quick-start"},
			{Host: "api.z.ai", Billing: Direct, Name: "zai-api", Source: "https://docs.z.ai/guides/overview/pricing"},
		},
		Opencode:  []OpencodeID{{ID: "zai-coding-plan", Endpoint: "zai"}, {ID: "zai", Endpoint: "zai-api"}},
		ModelsDev: []string{"zai"},
		Docs:      Docs{Label: "z.ai GLM Coding Plan", CatalogLabel: "z.ai GLM Coding"},
		// Coding plans sold by model vendors and gateways (ADR 0009). Read on
		// the vendor's own pages on 2026-10-01; a tier whose price is only on
		// a storefront TokenOps could not read carries no list price, and
		// plan cost uses what the operator records with --price.
		Plans: []Plan{
			{
				Name: "zai-glm-coding-lite", Display: "z.ai GLM Coding Lite",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "credits",
				SourceURL:   "https://docs.z.ai/devpack/overview (2026-10-01): 2,000 credits per 5 hours, 10,000 per week",
				MonthlyUSD:  18,
				PriceSource: "https://docs.z.ai/devpack/overview (2026-10-01): starting at 18 USD per month",
			},
			{
				Name: "zai-glm-coding-pro", Display: "z.ai GLM Coding Pro",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "credits",
				SourceURL: "https://docs.z.ai/devpack/overview (2026-10-01): 12,000 credits per 5 hours, 60,000 per week; price only on the storefront",
			},
			{
				Name: "zai-glm-coding-max", Display: "z.ai GLM Coding Max",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "credits",
				SourceURL: "https://docs.z.ai/devpack/overview (2026-10-01): 28,000 credits per 5 hours, 140,000 per week; price only on the storefront",
			},
		},
	}
}
