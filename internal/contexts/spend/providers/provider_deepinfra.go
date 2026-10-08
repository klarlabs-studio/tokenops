package providers

func providerDeepInfra() Descriptor {
	return Descriptor{
		ID:          "deepinfra",
		DisplayName: "DeepInfra",
		Sources: []Source{
			{Name: "deepinfra_account", Tag: "deepinfra-account", Kind: Spend, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /payment/checklist`", Shows: "spend since the last invoice, the limit, prepaid credit"},
		},
		Endpoints: []Endpoint{{Host: "api.deepinfra.com", Billing: Reseller, Source: "https://deepinfra.com/docs/openai_api"}},
		Opencode:  []OpencodeID{{ID: "deepinfra", Endpoint: "deepinfra"}},
		EnvVars:   []string{"DEEPINFRA_API_KEY"},
	}
}
