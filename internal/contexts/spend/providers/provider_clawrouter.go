package providers

func providerClawRouter() Descriptor {
	return Descriptor{
		ID:          "clawrouter",
		DisplayName: "ClawRouter",
		Sources: []Source{
			{Name: "clawrouter_gateway", Tag: "clawrouter-account", Kind: Gateway, Credential: APIKey,
				Switch: SwitchAccounts, Reader: GatewayReader, Verified: FromDocs,
				RecognisedBy: "its host, or `GET /v1/health`",
				Endpoint:     "`GET /v1/usage`", Shows: "the policy's spend against its monthly budget"},
		},
	}
}
