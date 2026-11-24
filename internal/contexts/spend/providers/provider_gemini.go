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
				Shows:   "per-turn tokens per model from Gemini CLI's chat recordings (Gemini Code Assist Standard or Enterprise, or an API key)"},
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/gemini-web.ts
			// (0ff1cf55c0, a26ca57b). The Gemini app's own limits for Google
			// AI Pro and Ultra, from the usage page's query with the Google
			// browser session; Google publishes no API for them. Not verified
			// against a live account.
			{Name: "gemini_web", Tag: "gemini-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Resources/Plugins/gemini-web.ts",
				Cookie: &Cookie{Host: "gemini.google.com", AllForHost: true,
					Proof: []string{"SID", "__Secure-1PSID", "__Secure-3PSID"}},
				Endpoint: "`GET /_/BardChatUi/graphql/schemas/GEMINI_WEB_GRAPHQL/executeQuery` (UsagePageQuery, gemini.google.com)",
				Shows:    "the Gemini app's 5-hour and weekly limits (Google AI Pro, Ultra)"},
		},
		Opencode: []OpencodeID{{ID: "google"}, {ID: "gemini"}, {ID: "google-vertex"}},
		// Gemini CLI serves Gemini Code Assist Standard and Enterprise
		// licences and paid API keys. Since 2026-06-18 it no longer serves
		// Google AI Pro, Ultra or the free individual tier, which moved to
		// Antigravity CLI (developers.googleblog.com, "An important update:
		// transitioning Gemini CLI to Antigravity CLI").
		Docs: Docs{Setup: "`vendor_usage.gemini_cli.enabled: true`, for Gemini CLI with a Gemini Code Assist Standard or Enterprise licence or an API key; Google AI Pro and Ultra moved to Antigravity CLI on 2026-06-18 (see Antigravity); `tokenops vendor-usage setup gemini` reads the Gemini app's 5-hour and weekly limits with your Google browser session"},
		Plans: []Plan{
			// Google AI Pro (formerly Google One AI Premium; the old
			// catalog name gemini-ai-premium is an alias) — fixed monthly
			// subscription with no published message or token caps; Google
			// throttles dynamically. Modeled without a window so headroom math
			// reports consumption trends instead of a cap (mirrors gpt-pro).
			{
				Name:        "gemini-ai-pro",
				Display:     "Google AI Pro",
				SourceURL:   "https://gemini.google/subscriptions/ (2026-09-30)",
				MonthlyUSD:  19.99,
				PriceSource: "https://gemini.google/subscriptions/ (2026-09-30): Google AI Pro $19.99 / month",
				ReadBy:      "antigravity-local",
				ReadByWhy:   "Gemini CLI stopped serving Google AI Pro on 2026-06-18; it moved to Antigravity CLI",
			},
		},
	}
}
