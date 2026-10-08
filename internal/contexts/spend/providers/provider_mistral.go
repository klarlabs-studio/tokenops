package providers

import (
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func providerMistral() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderMistral,
		DisplayName: "Mistral",
		Logo:        true,
		// Ported from CodexBar's Mistral provider
		// (Sources/CodexBarCore/Providers/Mistral/MistralUsageFetcher.swift,
		// MistralSubscriptionBudgetParser.swift, MistralCookieImporter.swift).
		Sources: []Source{
			{Name: "mistral_web", Tag: "mistral-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "admin.mistral.ai", Names: []string{"ory_session_*", "csrftoken"},
					Proof: []string{"ory_session_*"}},
				Endpoint: "`GET /api/billing/v2/usage`, `/subscription`, `/api/billing/credits` (admin.mistral.ai)",
				Shows:    "the included-API and Vibe allowances' shares used this month, and the credit balance when in dollars"},
		},
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
