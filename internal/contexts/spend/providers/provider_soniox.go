package providers

func providerSoniox() Descriptor {
	return Descriptor{
		ID:          "soniox",
		DisplayName: "Soniox",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/soniox.ts,
			// Providers/Soniox, docs/soniox.md (a26ca57b); Soniox's
			// Organization API (soniox.com/docs/guides/organization-api).
			// Not verified against a live account.
			{Name: "soniox_organization", Tag: "soniox-organization", Kind: Spend, Credential: AdminKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				EnvVars:   []string{"SONIOX_ORG_API_KEY"},
				KeyFormat: "an organisation API key with Balance and spend and Usage and limits access",
				Endpoint:  "`GET /v1/organization/balance`, `GET /v1/organization/usage/monthly`",
				Shows:     "the prepaid USD balance and this month's spend against the organisation's budget"},
		},
		Endpoints: []Endpoint{{Host: "api.soniox.com", Billing: Direct, Source: "https://soniox.com/docs/guides/organization-api"}},
	}
}
