package providers

import "time"

func providerSynthetic() Descriptor {
	return Descriptor{
		ID:          "synthetic",
		DisplayName: "Synthetic",
		Sources: []Source{
			{Name: "synthetic_account", Tag: "synthetic-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /v2/quotas`", Shows: "the subscription's request quota"},
		},
		Endpoints: []Endpoint{{Host: "api.synthetic.new", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "synthetic", Endpoint: "synthetic"}},
		EnvVars:   []string{"SYNTHETIC_API_KEY"},
		ModelsDev: []string{"synthetic"},
		Docs:      Docs{CatalogLabel: "Synthetic"},
		Plans: []Plan{
			{
				Name: "synthetic-pack", Display: "Synthetic subscription pack",
				RateLimitWindow: 5 * time.Hour, MessagesPerWindow: 500, WindowUnit: "requests",
				SourceURL:   "https://synthetic.new/pricing (2026-10-01): 500 requests per 5 hours per pack",
				MonthlyUSD:  30,
				PriceSource: "https://synthetic.new/pricing (2026-10-01): $30 per pack per month",
			},
		},
	}
}
