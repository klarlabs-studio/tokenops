package providers

func providerGoogleDeveloper() Descriptor {
	return Descriptor{
		ID:          "google-developer",
		DisplayName: "Google Developer Program",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/googledeveloper.ts,
			// Providers/GoogleDeveloper, docs/googledeveloper.md (a26ca57b).
			// The monthly Gen AI & Cloud credit Google AI Pro and Ultra
			// include, applied to a Google Cloud billing account: a credit
			// beside the Gemini plan, not the plan, read from the benefits
			// page (Google publishes no API for it). Only the current
			// month's grant is in the answer. Not verified against a live
			// account.
			{Name: "google_developer", Tag: "google-developer", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Resources/Plugins/googledeveloper.ts (docs/googledeveloper.md)",
				Cookie: &Cookie{Host: "me.developers.google.com", AllForHost: true,
					Proof: []string{"SID", "__Secure-1PSID", "__Secure-3PSID"}},
				Endpoint: "`GET /benefits`, `POST /_/GoogleDeveloperProfile/data/batchexecute` (rpcid Ro0AYc)",
				Shows:    "what is left of this month's Gen AI & Cloud credit (Google AI Pro, Ultra)"},
		},
	}
}
