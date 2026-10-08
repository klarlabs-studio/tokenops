package providers

import (
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func providerMistral() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderMistral,
		DisplayName: "Mistral",
		CatalogOnly: "Le Chat Pro's plan and daily cap are known; nothing reads its usage yet",
		Plans: []Plan{
			// Mistral Le Chat Pro — fixed monthly subscription, daily message
			// cap published in 2025-Q4. Window unit is "messages per day";
			// modeled as a 24h rolling window for headroom math parity with
			// the other consumer plans.
			{
				Name:              "mistral-le-chat-pro",
				Display:           "Mistral Le Chat Pro",
				RateLimitWindow:   24 * time.Hour,
				MessagesPerWindow: 200,
				WindowUnit:        "messages",
				SourceURL:         "https://mistral.ai/pricing (2026-05)",
				MonthlyUSD:        14.99,
				PriceSource:       "https://mistral.ai/pricing (2026-09-30): Pro $14.99 per month",
			},
		},
	}
}
