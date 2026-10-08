package providers

func providerLongCat() Descriptor {
	return Descriptor{
		ID:          "longcat",
		DisplayName: "LongCat",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/longcat.ts,
			// Providers/LongCat, docs/longcat.md. LongCat's API keys read no
			// usage; the platform's web calls are read with the session, whose
			// cookies are not known by name, so the Cookie header is pasted.
			// PasteOnly: no proof cookie is known to find a signed-in browser by.
			{Name: "longcat_account", Tag: "longcat-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "longcat.chat", PasteOnly: true},
				Endpoint: "`POST /api/pay/quota/metering/token-packs/summary`",
				Shows:    "the token pack's share used, and the tokens left with pending fuel packs"},
		},
		EnvVars: []string{"LONGCAT_MANUAL_COOKIE"},
	}
}
