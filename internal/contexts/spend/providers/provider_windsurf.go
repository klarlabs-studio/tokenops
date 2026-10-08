package providers

func providerWindsurf() Descriptor {
	return Descriptor{
		ID:          "windsurf",
		DisplayName: "Windsurf",
		Logo:        true,
		Sources: []Source{
			{Name: "windsurf_local", Tag: "windsurf-local", Kind: Subscription, Credential: LocalFile,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Windsurf/WindsurfStatusProbe.swift (docs/windsurf.md)",
				Endpoint:  "Windsurf's `state.vscdb` (`windsurf.settings.cachedPlanInfo`)",
				Shows:     "daily and weekly quota (or messages and flow actions) as Windsurf last cached them"},
			{Name: "windsurf_web", Tag: "windsurf-web", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Windsurf/WindsurfWebFetcher.swift (docs/windsurf.md)",
				Prompt: "the Windsurf session bundle as one line of JSON (devin_session_token, devin_auth1_token, " +
					"devin_account_id, devin_primary_org_id from windsurf.com's localStorage)",
				Endpoint: "`POST windsurf.com/_backend/.../GetPlanStatus`",
				Shows:    "daily and weekly quota, live"},
		},
		Docs: Docs{Setup: "the cached plan is read with nothing to do; `tokenops vendor-usage setup windsurf` takes the web session for live figures"},
	}
}
