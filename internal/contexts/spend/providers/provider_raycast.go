package providers

func providerRaycast() Descriptor {
	return Descriptor{
		ID:          "raycast",
		DisplayName: "Raycast",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/raycast.ts,
			// Providers/Raycast, docs/raycast.md. The credits route is the
			// website's own, read with its session.
			{Name: "raycast_account", Tag: "raycast-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "www.raycast.com", Names: []string{"__raycast_session", "csrf_token"}},
				Endpoint: "`GET /frontend_api/current_user/ai_credits`",
				Shows:    "the month's AI credit allowance used, and the credits left"},
		},
	}
}
