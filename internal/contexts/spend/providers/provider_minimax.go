package providers

import "time"

func providerMiniMax() Descriptor {
	return Descriptor{
		ID:          "minimax",
		DisplayName: "MiniMax",
		Logo:        true,
		Sources: []Source{
			{Name: "minimax_account", Tag: "minimax-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromClientSource,
				Endpoint: "`GET /v1/token_plan/remains`", Shows: "the interval and weekly windows"},
		},
		// MiniMax serves its Token Plan and pay-as-you-go on one host; the key
		// decides, which TokenOps does not read. A bound plan is taken to
		// cover it.
		Endpoints: []Endpoint{{Host: "api.minimax.io", Billing: Direct, Source: "https://platform.minimax.io/docs/token-plan/quickstart"}},
		Opencode: []OpencodeID{
			{ID: "minimax-coding-plan", Endpoint: "minimax"},
			{ID: "minimax-cn-coding-plan", Endpoint: "minimax"},
			{ID: "minimax", Endpoint: "minimax-api"},
			{ID: "minimax-cn", Endpoint: "minimax-api"},
		},
		ModelsDev: []string{"minimax"},
		Docs:      Docs{Label: "MiniMax Token Plan", CatalogLabel: "MiniMax Token Plan"},
		Plans: []Plan{
			{
				Name: "minimax-token-plus", Display: "MiniMax Token Plan Plus",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "requests",
				SourceURL:   "https://platform.minimax.io/docs/token-plan/intro (2026-10-01): rolling 5-hour and weekly windows, no published count",
				MonthlyUSD:  22,
				PriceSource: "https://platform.minimax.io/docs/token-plan/intro (2026-10-01): Plus $22 per month",
			},
			{
				Name: "minimax-token-max", Display: "MiniMax Token Plan Max",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "requests",
				SourceURL:   "https://platform.minimax.io/docs/token-plan/intro (2026-10-01): rolling 5-hour and weekly windows, no published count",
				MonthlyUSD:  55,
				PriceSource: "https://platform.minimax.io/docs/token-plan/intro (2026-10-01): Max $55 per month",
			},
			{
				Name: "minimax-token-ultra", Display: "MiniMax Token Plan Ultra",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "requests",
				SourceURL:   "https://platform.minimax.io/docs/token-plan/intro (2026-10-01): rolling 5-hour and weekly windows, no published count",
				MonthlyUSD:  132,
				PriceSource: "https://platform.minimax.io/docs/token-plan/intro (2026-10-01): Ultra $132 per month",
			},
		},
	}
}
