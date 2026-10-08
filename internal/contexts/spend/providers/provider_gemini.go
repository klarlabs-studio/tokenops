package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerGemini() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderGemini,
		DisplayName: "Gemini",
		PlanPrefix:  "Gemini ",
		Logo:        true,
		Sources: []Source{
			{Name: "gemini_cli", Tag: "gemini-cli", Kind: LocalLog, Credential: LocalFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: FromDocs,
				Package: "internal/contexts/spend/vendorusage/geminicli",
				Fixture: "internal/contexts/spend/vendorusage/geminicli/reader_test.go",
				Shows:   "per-turn tokens per model from Gemini CLI's chat recordings"},
		},
		Opencode: []OpencodeID{{ID: "google"}, {ID: "gemini"}, {ID: "google-vertex"}},
		Docs:     Docs{Setup: "`vendor_usage.gemini_cli.enabled: true`"},
		Plans: []Plan{
			// Google One AI Premium (Gemini Advanced) — fixed monthly
			// subscription with no published message or token caps; Google
			// throttles dynamically. Modeled without a window so headroom math
			// reports consumption trends instead of a cap (mirrors gpt-pro).
			{
				Name:        "gemini-ai-premium",
				Display:     "Google One AI Premium",
				SourceURL:   "https://one.google.com/about/ai-premium (2026-05)",
				MonthlyUSD:  19.99,
				PriceSource: "https://gemini.google/subscriptions/ (2026-09-30): Google AI Pro $19.99 / month",
			},
		},
	}
}
