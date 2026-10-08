package providers

import "time"

func providerOpencodeGo() Descriptor {
	return Descriptor{
		ID:          "opencode-go",
		DisplayName: "opencode Go",
		Logo:        true,
		// Its turns are also in opencode's store (the opencode source), which
		// is where CodexBar reads its local cost history from.
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/OpenCodeGo/
			// OpenCodeGoUsageFetcher.swift (fetchAPIUsage), docs/opencodego.md.
			{Name: "opencode_go_account", Tag: "opencode-go-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /zen/go/v1/usage`", Shows: "the 5-hour, weekly and monthly windows"},
		},
		Endpoints: []Endpoint{{Host: "opencode.ai", Path: "/zen/go", Billing: Reseller, Source: "https://opencode.ai/docs/go"}},
		Opencode:  []OpencodeID{{ID: "opencode-go", Endpoint: "opencode-go"}},
		EnvVars:   []string{"OPENCODE_API_KEY"},
		ModelsDev: []string{"opencode-go"},
		Docs:      Docs{CatalogLabel: "opencode Go"},
		Plans: []Plan{
			{
				Name: "opencode-go", Display: "opencode Go",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "usd",
				SourceURL:   "https://opencode.ai/docs/go (2026-10-01): a dollar cap per model per month; 5h = 20%, week = 50% of it",
				MonthlyUSD:  10,
				PriceSource: "https://opencode.ai/docs/go (2026-10-01): Go $10 per month",
			},
			{
				Name: "opencode-go-plus", Display: "opencode Go Plus",
				RateLimitWindow: 5 * time.Hour, WindowUnit: "usd",
				SourceURL:   "https://opencode.ai/docs/go (2026-10-01): a dollar cap per model per month; 5h = 20%, week = 50% of it",
				MonthlyUSD:  40,
				PriceSource: "https://opencode.ai/docs/go (2026-10-01): Go Plus $40 per month",
			},
		},
	}
}
