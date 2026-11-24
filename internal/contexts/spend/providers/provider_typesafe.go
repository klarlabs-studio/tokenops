package providers

func providerTypeSafe() Descriptor {
	return Descriptor{
		ID:          "typesafe",
		DisplayName: "TypeSafe",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/typesafe.ts,
			// Providers/TypeSafe, docs/typesafe.md (b22cdc2fd7). Inference keys
			// read no billing; the console's billing overview is read with its session,
			// whose cookie is not known by name, so the Cookie header is
			// pasted.
			// PasteOnly: no proof cookie is known to find a signed-in browser by.
			{Name: "typesafe_account", Tag: "typesafe-account", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "console.typesafe.ai", PasteOnly: true},
				Endpoint: "`GET /api/billing/overview`",
				Shows:    "spend this billing cycle and the credit balance left"},
		},
	}
}
