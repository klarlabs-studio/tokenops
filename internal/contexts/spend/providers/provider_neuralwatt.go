package providers

func providerNeuralWatt() Descriptor {
	return Descriptor{
		ID:          "neuralwatt",
		DisplayName: "Neuralwatt",
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/neuralwatt.js,
			// Providers/NeuralWatt, docs/neuralwatt.md) and its fixtures; not
			// verified against a live account.
			{Name: "neuralwatt_account", Tag: "neuralwatt-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromClientSource,
				Endpoint: "`GET /v1/quota`", Shows: "prepaid USD credit left, and the subscription's kWh allowance used this period"},
		},
		Endpoints: []Endpoint{{Host: "api.neuralwatt.com", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "neuralwatt", Endpoint: "neuralwatt"}},
		EnvVars:   []string{"NEURALWATT_API_KEY"},
		ModelsDev: []string{"neuralwatt"},
	}
}
