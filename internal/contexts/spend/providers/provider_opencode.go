package providers

func providerOpencode() Descriptor {
	return Descriptor{
		ID:          "opencode",
		DisplayName: "opencode",
		Sources: []Source{
			// opencode's store records turns for every provider it routes to,
			// so it grades every provider's headroom.
			{Name: "opencode", Tag: "opencode", Kind: LocalLog, Credential: LocalFile, AnyProvider: true,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/opencode",
				Fixture: "internal/contexts/spend/vendorusage/opencode/reader_test.go",
				Shows:   "per-message tokens for every provider, from opencode's SQLite store"},
		},
		// Zen is pay-per-token credits reselling other vendors' models; Go
		// (opencode-go) is the subscription under /zen/go.
		Endpoints: []Endpoint{{Host: "opencode.ai", Billing: Reseller, Source: "https://opencode.ai/docs/zen"}},
		Opencode:  []OpencodeID{{ID: "opencode", Endpoint: "opencode"}},
		ModelsDev: []string{"opencode"},
		// Zen's pricing table lists its free models as Free
		// (https://opencode.ai/docs/zen); models.dev lists them at $0.
		ModelsDevZeroIsFree: true,
		Docs:                Docs{Setup: "`vendor_usage.opencode.enabled: true`"},
	}
}
