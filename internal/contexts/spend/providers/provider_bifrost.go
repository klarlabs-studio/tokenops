package providers

func providerBifrost() Descriptor {
	return Descriptor{
		ID:          "bifrost",
		DisplayName: "Bifrost",
		Sources: []Source{
			{Name: "bifrost_gateway", Tag: "bifrost-account", Kind: Gateway, Credential: APIKey,
				Switch: SwitchAccounts, Reader: GatewayReader, Verified: FromDocs,
				RecognisedBy: "`GET /health`",
				Endpoint:     "`GET /api/governance/virtual-keys/quota`", Shows: "each budget's share used"},
		},
	}
}
