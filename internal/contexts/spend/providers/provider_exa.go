package providers

func providerExa() Descriptor {
	return Descriptor{
		ID:          "exa",
		DisplayName: "Exa",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/exa.js,
			// Providers/Exa, docs/exa.md (a26ca57b); Exa's Team Management
			// API (exa.ai/docs/reference/team-management/get-api-key-usage),
			// which Exa support enables per team. Ordinary search keys cannot
			// read it. Not verified against a live account.
			{Name: "exa_key", Tag: "exa-key", Kind: Spend, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "API_KEY_ID:SERVICE_KEY",
				Endpoint:  "`GET /team-management/api-keys/{id}/usage` (admin-api.exa.ai)",
				Shows:     "one API key's spend this month (UTC)"},
		},
		Endpoints: []Endpoint{{Host: "api.exa.ai", Billing: Direct, Source: "https://exa.ai/docs/admin/team-management"}},
		Docs:      Docs{Setup: "`tokenops vendor-usage setup exa` with the ID of the API key to watch and a Team Management service key, as API_KEY_ID:SERVICE_KEY (Exa support enables Team Management)"},
	}
}
