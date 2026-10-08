package providers

func providerKiro() Descriptor {
	return Descriptor{
		ID:          "kiro",
		DisplayName: "Kiro",
		Logo:        true,
		Sources: []Source{
			{Name: "kiro_cli", Tag: "kiro-cli", Kind: Subscription, Credential: CLI,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Kiro/KiroStatusProbe.swift (docs/kiro.md)",
				Endpoint:  "`kiro-cli chat --no-interactive /usage`",
				Shows:     "monthly plan credits used, and bonus credits"},
			// Overage needs kiro-cli's own sign-in and profile, read from its
			// database only once granted (ADR 0013); kiro-cli renews the
			// token. kirocli:odic:token is kiro-cli's own key (sic).
			{Name: "kiro_overage", Tag: "kiro-overage", Kind: Subscription, Credential: AppLogin,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Kiro/KiroUsageLimitsAPI.swift (docs/kiro.md)",
				AppLogins: []AppLoginItem{{App: "kiro-cli", Kind: AppLoginSQLite,
					Paths: []string{"~/Library/Application Support/kiro-cli/data.sqlite3", "~/.local/share/kiro-cli/data.sqlite3"},
					Query: "SELECT (SELECT json_extract(value, '$.access_token') FROM auth_kv WHERE key = 'kirocli:odic:token'), " +
						"(SELECT json_extract(value, '$.arn') FROM state WHERE key = 'api.codewhisperer.profile')",
					Fields: []string{"access_token", "profile_arn"},
					Host:   "codewhisperer.us-east-1.amazonaws.com or q.eu-central-1.amazonaws.com (by the profile's region)"}},
				Endpoint: "`POST GetUsageLimits` (CodeWhisperer)",
				Shows:    "plan credits used this month, and overage credits against their cap"},
		},
		Docs: Docs{Setup: "nothing: read with the operator's signed-in kiro-cli (`kiro-cli login`); overage with `tokenops vendor-usage setup kiro --use-app-login`"},
	}
}
