package providers

func providerTypeSafe() Descriptor {
	return Descriptor{
		ID:          "typesafe",
		DisplayName: "TypeSafe",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/typesafe.ts,
			// Providers/TypeSafe, docs/typesafe.md. Inference keys read no
			// billing; the console's billing action is read with its session,
			// whose cookie is not known by name, so the Cookie header is
			// pasted.
			{Name: "typesafe_account", Tag: "typesafe-account", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "console.typesafe.ai"},
				Endpoint: "`POST /settings/billing` (getBillingOverview action)",
				Shows:    "spend this billing cycle and the credit balance left"},
		},
	}
}
