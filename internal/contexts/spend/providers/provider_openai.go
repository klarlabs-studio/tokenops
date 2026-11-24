package providers

import (
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func providerOpenAI() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderOpenAI,
		DisplayName: "Codex",
		PlanPrefix:  "ChatGPT ",
		Logo:        true,
		Presence:    []Presence{{Dir: "~/.codex"}, {Binary: "codex"}, {App: "Codex.app"}},
		Sources: []Source{
			{Name: "codex_app_server", Tag: "codex-app-server", Kind: Subscription, Credential: CLI,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/codexappserver",
				Fixture: "internal/contexts/spend/vendorusage/codexappserver/codexappserver_test.go",
				Shows:   "the windows `codex app-server` reports (5-hour and weekly on Plus, weekly only on Pro), Codex signing its own request"},
			{Name: "codex_jsonl", Tag: "codex-jsonl", Kind: LocalLog, Credential: LocalFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package:          "internal/contexts/spend/vendorusage/codexjsonl",
				Fixture:          "internal/contexts/spend/vendorusage/codexjsonl/reader_test.go",
				Shows:            "per-turn tokens and the rate_limits in Codex's rollouts",
				WindowsInRecords: true},
			// The API Platform organisation's spend, with an admin key the
			// operator mints; API keys the harnesses use are never sent here.
			{Name: "openai_admin", Tag: "openai-admin", Kind: Spend, Credential: AdminKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Resources/Plugins/openai.js (docs/openai.md)",
				EnvVars:   []string{"OPENAI_ADMIN_KEY"},
				KeyFormat: "an organisation admin key (sk-admin-...)",
				Endpoint:  "`GET /v1/organization/costs`", Shows: "the API organisation's spend this month (Administration API)"},
		},
		Opencode: []OpencodeID{{ID: "openai"}},
		Docs: Docs{
			Label: "OpenAI",
			Setup: "nothing: the app server is asked when `codex` is installed; `vendor_usage.codex_jsonl.enabled: true` reads the rollouts; " +
				"`tokenops vendor-usage setup openai` (or `OPENAI_ADMIN_KEY`) reads the API organisation's spend with an admin key",
		},
		Plans: []Plan{
			{
				Name:            "gpt-plus",
				Display:         "ChatGPT Plus",
				RateLimitWindow: 5 * time.Hour,
				WindowUnit:      "messages",
				SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): local-message estimates are model-dependent per five-hour window",
				MonthlyUSD:      20,
				PriceSource:     "https://help.openai.com/en/articles/6950777 (2026-09-30): $20/month billed monthly",
				VendorPlanTypes: []string{"plus"},
			},
			// OpenAI lifted the five-hour limit from every Pro tier in Work and
			// Codex: each has only an included weekly allowance, so its shortest
			// window is the week, as on Claude Ultra. Codex reports it as a
			// single 10,080-minute primary window.
			{
				Name:            "gpt-pro",
				Display:         "ChatGPT Pro (tier unspecified)",
				NoSessionLimit:  true,
				RateLimitWindow: 7 * 24 * time.Hour,
				WindowUnit:      "messages",
				SourceURL: "https://help.openai.com/en/articles/20001516-managing-usage-with-gpt-6-astra-in-work-and-codex (2026-11-15): " +
					"\"Pro 100, Pro 200 and Pro 500 currently have no five-hour usage limit in Work and Codex. Your plan still has an included usage allowance.\"",
			},
			// OpenAI names the tiers Pro 100, Pro 200 and Pro 500. The catalog
			// names keep the multipliers they had when they were added, so
			// existing configs resolve; the weekly multiples are what OpenAI
			// states now. Codex reports Pro 100 as plan_type "prolite".
			{
				Name:             "gpt-pro-5x",
				Display:          "ChatGPT Pro 100",
				RelativeTo:       "gpt-plus",
				WeeklyMultiplier: 10,
				NoSessionLimit:   true,
				RateLimitWindow:  7 * 24 * time.Hour,
				WindowUnit:       "messages",
				SourceURL: "https://help.openai.com/en/articles/20001516-managing-usage-with-gpt-6-astra-in-work-and-codex (2026-11-15): no five-hour usage limit in Work and Codex; " +
					"10x Plus usage as stated by OpenAI's Codex lead (post on X, 2026-11-08); " +
					"https://help.openai.com/en/articles/9793128-about-chatgpt-pro-tiers (2026-11-15): twice its previous Work and Codex allowance",
				MonthlyUSD:      100,
				PriceSource:     "https://help.openai.com/en/articles/9793128-about-chatgpt-pro-tiers (2026-11-15): Pro 100, $100 per month",
				VendorPlanTypes: []string{"prolite"},
			},
			{
				Name:             "gpt-pro-20x",
				Display:          "ChatGPT Pro 200",
				RelativeTo:       "gpt-plus",
				WeeklyMultiplier: 20,
				NoSessionLimit:   true,
				RateLimitWindow:  7 * 24 * time.Hour,
				WindowUnit:       "messages",
				SourceURL: "https://help.openai.com/en/articles/20001516-managing-usage-with-gpt-6-astra-in-work-and-codex (2026-11-15): no five-hour usage limit in Work and Codex; " +
					"20x Plus usage as stated by OpenAI's Codex lead (post on X, 2026-11-08); " +
					"https://help.openai.com/en/articles/9793128-about-chatgpt-pro-tiers (2026-11-15): only the lower allowance doubled, all Pro 200 subscriptions share the same allowance",
				MonthlyUSD:      200,
				PriceSource:     "https://help.openai.com/en/articles/9793128-about-chatgpt-pro-tiers (2026-11-15): Pro 200, $200 per month",
				VendorPlanTypes: []string{"pro"},
			},
			// No Codex plan_type has been observed for Pro 500 yet.
			{
				Name:             "gpt-pro-500",
				Display:          "ChatGPT Pro 500",
				RelativeTo:       "gpt-plus",
				WeeklyMultiplier: 50,
				NoSessionLimit:   true,
				RateLimitWindow:  7 * 24 * time.Hour,
				WindowUnit:       "messages",
				SourceURL: "https://help.openai.com/en/articles/20001516-managing-usage-with-gpt-6-astra-in-work-and-codex (2026-11-15): no five-hour usage limit in Work and Codex; " +
					"50x Plus usage as stated by OpenAI's Codex lead (post on X, 2026-11-08); " +
					"https://help.openai.com/en/articles/9793128-about-chatgpt-pro-tiers (2026-11-15): twice its previous Work and Codex allowance, the only Pro tier with Ultrafast",
				MonthlyUSD:  500,
				PriceSource: "https://help.openai.com/en/articles/9793128-about-chatgpt-pro-tiers (2026-11-15): Pro 500, $500 per month",
			},
			{
				Name:            "gpt-business",
				Display:         "ChatGPT Business",
				RateLimitWindow: 5 * time.Hour,
				WindowUnit:      "messages",
				SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): Standard Business is the current workspace plan name",
				MonthlyUSD:      25,
				PerSeat:         true,
				PriceSource:     "https://help.openai.com/en/articles/8542115 (2026-09-30): $25 per user per month on monthly billing",
			},
		},
	}
}
