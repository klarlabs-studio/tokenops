package providers

func providerOpenDesign() Descriptor {
	return Descriptor{
		ID:          "opendesign",
		DisplayName: "OpenDesign",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/opendesign.js,
			// Providers/OpenDesign, docs/opendesign.md (a26ca57b). Undocumented
			// endpoints the dashboard calls, with its browser session;
			// inference keys cannot read billing. Not verified against a live
			// account.
			{Name: "opendesign_web", Tag: "opendesign-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "amr-api.open-design.ai", Names: []string{"__Secure-better-auth.session_token*"},
					Proof: []string{"__Secure-better-auth.session_token*"}},
				Endpoint: "`GET /api/v1/workspaces/{id}/billing/coding-plan-usage` (amr-api.open-design.ai)",
				Shows:    "the active workspace's Coding Plan 5-hour and weekly windows"},
		},
	}
}
