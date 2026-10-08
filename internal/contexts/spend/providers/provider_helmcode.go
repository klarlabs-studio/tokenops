package providers

func providerHelmcode() Descriptor {
	return Descriptor{
		ID:          "helmcode",
		DisplayName: "Helmcode",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/helmcode.ts,
			// Providers/Helmcode, docs/helmcode.md. Inference keys read no
			// quota; the dashboard API is read with its session, whose
			// cookies are not known by name, so the Cookie header is pasted.
			// PasteOnly: no proof cookie is known to find a signed-in browser by.
			{Name: "helmcode_account", Tag: "helmcode-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "cloud.helmcode.com", PasteOnly: true},
				Endpoint: "`GET /api/usage/quota`",
				Shows:    "each model's token quota used, and the prepaid balance"},
		},
	}
}
