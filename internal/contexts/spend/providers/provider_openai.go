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
		Sources: []Source{
			{Name: "codex_app_server", Tag: "codex-app-server", Kind: Subscription, Credential: CLI,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/codexappserver",
				Fixture: "internal/contexts/spend/vendorusage/codexappserver/codexappserver_test.go",
				Shows:   "the 5-hour and weekly windows `codex app-server` reports, Codex signing its own request"},
			{Name: "codex_jsonl", Tag: "codex-jsonl", Kind: LocalLog, Credential: LocalFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/codexjsonl",
				Fixture: "internal/contexts/spend/vendorusage/codexjsonl/reader_test.go",
				Shows:   "per-turn tokens and the rate_limits in Codex's rollouts"},
			// The API Platform organisation's spend, with an admin key the
			// operator mints; API keys the harnesses use are never sent here.
			{Name: "openai_admin", Tag: "openai-admin", Kind: Spend, Credential: AdminKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Resources/Plugins/openai.js (docs/openai.md)",
				EnvVars:   []string{"OPENAI_ADMIN_KEY"},
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
			{
				Name:            "gpt-pro",
				Display:         "ChatGPT Pro (tier unspecified)",
				RateLimitWindow: 5 * time.Hour,
				WindowUnit:      "messages",
				SourceURL:       "https://chatgpt.com/pricing (2026-10-01, in-app pricing screen): Pro is offered at $100, $200 and $500, labelled Standard, More usage and Max usage",
			},
			// The three Pro tiers carry OpenAI's labels from the pricing screen
			// and their US price, which is what stays fixed: OpenAI moved the $200
			// tier from 20x to 10x Plus usage at DevDay 2026 without changing its
			// price. The catalog names keep their original multipliers so existing
			// configs resolve. Codex reports the Standard tier as plan_type
			// "prolite".
			{
				Name:            "gpt-pro-5x",
				Display:         "ChatGPT Pro Standard ($100)",
				RateLimitWindow: 5 * time.Hour,
				WindowUnit:      "messages",
				SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): five times Plus Codex usage",
				MonthlyUSD:      100,
				PriceSource:     "https://help.openai.com/en/articles/9793128 (2026-09-30): Pro $100 unlocks 5x higher usage than Plus",
				VendorPlanTypes: []string{"prolite"},
			},
			{
				Name:            "gpt-pro-20x",
				Display:         "ChatGPT Pro More usage ($200)",
				RateLimitWindow: 5 * time.Hour,
				WindowUnit:      "messages",
				SourceURL:       "https://developers.openai.com/codex/pricing (2026-10-01): 20x Plus usage, falling to 10x for new subscriptions and from 2026-10-30 for existing ones (announced at DevDay, 2026-09-29)",
				MonthlyUSD:      200,
				PriceSource:     "https://help.openai.com/en/articles/9793128 (2026-09-30): Pro $200 unlocks 20x usage than Plus",
				VendorPlanTypes: []string{"pro"},
			},
			// Pro 500 ("Max usage" on the pricing screen) launched at DevDay
			// 2026. OpenAI describes its allowance as 25x Plus; no Codex plan_type
			// has been observed for it yet.
			{
				Name:            "gpt-pro-500",
				Display:         "ChatGPT Pro Max usage ($500)",
				RateLimitWindow: 5 * time.Hour,
				WindowUnit:      "messages",
				SourceURL:       "https://developers.openai.com/codex/pricing (2026-10-01): Pro 500, 25x Plus usage per OpenAI's DevDay recap, the only Pro tier with Astra Ultrafast",
				MonthlyUSD:      500,
				PriceSource:     "https://developers.openai.com/codex/pricing (2026-10-01): Pro plans at $100, $200, or $500 USD per month",
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
