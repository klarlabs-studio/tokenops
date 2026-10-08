package providers

func providerXKiro() Descriptor {
	return Descriptor{
		ID:          "xkiro",
		DisplayName: "xKiro",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/XKiro,
			// Resources/Plugins/xkiro.ts, docs/xkiro.md; the plan windows and
			// wallet from xKiro's own docs (https://docs.xkiro.com/api/usage/).
			{Name: "xkiro_account", Tag: "xkiro-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/usage`", Shows: "the plan's spend windows, today's free tokens and the wallet balance"},
		},
		Endpoints: []Endpoint{{Host: "api.xkiro.com", Billing: Reseller}},
		EnvVars:   []string{"XKIRO_API_KEY"},
	}
}
