package providers

func providerAixy() Descriptor {
	return Descriptor{
		ID:          "aixy",
		DisplayName: "Aixy",
		Sources: []Source{
			// A gateway on the customer's own provider credentials, hosted or
			// self-hosted: it bills nothing itself, so it has no Endpoints.
			{Name: "aixy_gateway", Tag: "aixy-account", Kind: Gateway, Credential: APIKey,
				Switch: SwitchAccounts, Reader: GatewayReader, Verified: FromCodexBar,
				Reference:      "CodexBar Sources/CodexBarCore/Resources/Plugins/aixy.ts (docs/aixy.md)",
				EnvVars:        []string{"AIXY_API_KEY"},
				BaseURLEnv:     "AIXY_BASE_URL",
				DefaultBaseURL: "https://api.aixy-gateway.com",
				RecognisedBy:   "the hosted host, or its name (`AIXY_API_KEY`, setup)",
				Endpoint:       "`GET /v1/usage`",
				Shows:          "each budget that applies to the key, spent and reserved against its limit"},
		},
	}
}
