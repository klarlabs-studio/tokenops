package providers

import (
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func providerCerebras() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderCerebras,
		DisplayName: "Cerebras",
		CatalogOnly: "Cerebras Code's plans and limits are known; nothing reads its usage yet",
		Docs:        Docs{CatalogLabel: "Cerebras Code"},
		Plans: []Plan{
			{
				Name: "cerebras-code-pro", Display: "Cerebras Code Pro",
				RateLimitWindow: 24 * time.Hour, WindowUnit: "tokens",
				SourceURL:   "https://www.cerebras.ai/code (2026-10-01): 24M tokens per day",
				MonthlyUSD:  50,
				PriceSource: "https://www.cerebras.ai/code (2026-10-01): Pro $50 per month",
			},
			{
				Name: "cerebras-code-max", Display: "Cerebras Code Max",
				RateLimitWindow: 24 * time.Hour, WindowUnit: "tokens",
				SourceURL:   "https://www.cerebras.ai/code (2026-10-01): 120M tokens per day",
				MonthlyUSD:  200,
				PriceSource: "https://www.cerebras.ai/code (2026-10-01): Max $200 per month",
			},
		},
	}
}
