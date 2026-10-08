package providers

func providerLithosAI() Descriptor {
	return Descriptor{
		ID:          "lithosai",
		DisplayName: "LithosAI",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/lithosai.ts
			// (Providers/LithosAI), docs/lithosai.md. Inference API keys
			// do not read the console's billing.
			{Name: "lithosai_web", Tag: "lithosai-web", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "console.lithosai.cloud", Names: []string{"__Host-console_session", "__Host-console_csrf"},
					Proof: []string{"__Host-console_session"}},
				Endpoint: "`GET /api/me`, `/api/billing`, `/api/billing/spend` (console.lithosai.cloud)",
				Shows:    "the organisation's prepaid balance and its spend this UTC month"},
		},
	}
}
