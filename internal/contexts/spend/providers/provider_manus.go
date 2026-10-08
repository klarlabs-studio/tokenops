package providers

func providerManus() Descriptor {
	return Descriptor{
		ID:          "manus",
		DisplayName: "Manus",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/manus.js,
			// Providers/Manus, docs/manus.md. Manus has no usage API; the
			// web app's own credits call is read with its session.
			{Name: "manus_account", Tag: "manus-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "manus.im", Names: []string{"session_id"}},
				Endpoint: "`POST /user.v1.UserService/GetAvailableCredits`",
				Shows:    "monthly and daily-refresh credits used, and the credit balance"},
		},
		EnvVars: []string{"MANUS_SESSION_TOKEN", "MANUS_COOKIE"},
	}
}
