package providers

func providerReplicate() Descriptor {
	return Descriptor{
		ID:          "replicate",
		DisplayName: "Replicate",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/replicate.ts,
			// Providers/Replicate, docs/replicate.md. Replicate's API token
			// reads no billing; the website's billing calls are read with its
			// session.
			{Name: "replicate_account", Tag: "replicate-account", Kind: Spend, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "replicate.com", Names: []string{"sessionid", "csrftoken"}},
				Endpoint: "`GET /api/{users|organizations}/{name}/invoices`",
				Shows:    "spend this month, and the prepaid credit left"},
		},
	}
}
