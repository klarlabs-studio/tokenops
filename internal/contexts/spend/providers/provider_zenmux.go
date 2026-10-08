package providers

func providerZenMux() Descriptor {
	return Descriptor{
		ID:          "zenmux",
		DisplayName: "ZenMux",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/ZenMux,
			// Resources/Plugins/zenmux.js, docs/zenmux.md. The reader takes a
			// Management API key, which only `vendor-usage setup zenmux`
			// supplies: the inference keys harnesses and opencode hold
			// (ZENMUX_API_KEY) are refused by the Management API, so none is
			// sent there, and ZENMUX_MANAGEMENT_API_KEY is not read because
			// a variable goes to the inference endpoint.
			{Name: "zenmux_account", Tag: "zenmux-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /api/v1/management/subscription/detail`",
				Shows:    "rolling 5-hour and 7-day quotas, and the pay-as-you-go balance"},
		},
		Endpoints: []Endpoint{{Host: "zenmux.ai", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "zenmux", Endpoint: "zenmux"}},
		ModelsDev: []string{"zenmux"},
		Docs:      Docs{Setup: "`tokenops vendor-usage setup zenmux` takes a Management API key (ZenMux console → Management)"},
	}
}
