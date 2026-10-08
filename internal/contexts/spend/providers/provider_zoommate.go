package providers

func providerZoomMate() Descriptor {
	return Descriptor{
		ID:          "zoommate",
		DisplayName: "ZoomMate",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/zoommate.ts,
			// Providers/ZoomMate, docs/zoommate.md. ZoomMate has no public
			// API; its web client's credits call is read with the Zoom
			// session, whose SSO cookies are not known by name, so the
			// Cookie header of a request to ai.zoom.us is pasted.
			{Name: "zoommate_account", Tag: "zoommate-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "ai.zoom.us"},
				Endpoint: "`GET /ai-computer/api/v1/credits/status`",
				Shows:    "AI credits used against the budget cap this billing cycle, and the credits left"},
		},
	}
}
