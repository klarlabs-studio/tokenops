package providers

func providerClinePass() Descriptor {
	return Descriptor{
		ID:          "clinepass",
		DisplayName: "ClinePass",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Providers/ClinePass,
			// Resources/Plugins/clinepass.ts, docs/clinepass.md. Cline's own
			// sign-in is read only once granted (`setup clinepass
			// --use-app-login`, ADR 0013).
			{Name: "clinepass_account", Tag: "clinepass-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				AppLogins: []AppLoginItem{{App: "Cline", Kind: AppLoginJSON,
					Paths: []string{"~/.cline/data/settings/providers.json"}, PathEnv: "CLINE_PROVIDER_SETTINGS_PATH",
					Fields: []string{"providers.cline.settings.auth.accessToken"}, Host: "api.cline.bot"}},
				Endpoint: "`GET /api/v1/users/me/plan/usage-limits`", Shows: "5-hour, weekly and monthly windows"},
		},
		Endpoints: []Endpoint{{Host: "api.cline.bot", Billing: Reseller}},
		Opencode:  []OpencodeID{{ID: "cline-pass", Endpoint: "clinepass"}},
		EnvVars:   []string{"CLINE_API_KEY", "CLINEPASS_API_KEY"},
		ModelsDev: []string{"cline-pass"},
	}
}
