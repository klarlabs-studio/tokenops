package providers

func providerSub2API() Descriptor {
	return Descriptor{
		ID:          "sub2api",
		DisplayName: "sub2api",
		Sources: []Source{
			// A self-hosted gateway: read where a harness sends a key to it,
			// where SUB2API_BASE_URL names it, or at the address setup stored.
			{Name: "sub2api_gateway", Tag: "sub2api-account", Kind: Gateway, Credential: APIKey,
				Switch: SwitchAccounts, Reader: GatewayReader, Verified: FromCodexBar,
				Reference:    "CodexBar Sources/CodexBarCore/Resources/Plugins/sub2api.js (docs/sub2api.md)",
				EnvVars:      []string{"SUB2API_API_KEY"},
				BaseURLEnv:   "SUB2API_BASE_URL",
				RecognisedBy: "`GET /setup/status`, or its name (`SUB2API_BASE_URL`, setup)",
				Endpoint:     "`GET /v1/usage`",
				Shows:        "the key's quota and 5-hour, daily and 7-day limits, a subscription group's daily, weekly and monthly limits, or the wallet balance"},
		},
	}
}
