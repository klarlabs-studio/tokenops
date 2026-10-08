package providers

func providerWarp() Descriptor {
	return Descriptor{
		ID:          "warp",
		DisplayName: "Warp",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/Warp
			// (WarpUsageFetcher.swift), docs/warp.md.
			{Name: "warp_account", Tag: "warp-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`POST /graphql/v2?op=GetRequestLimitInfo`",
				Shows:    "credits used since the last refresh against the plan's limit"},
		},
		EnvVars: []string{"WARP_API_KEY", "WARP_TOKEN"},
	}
}
