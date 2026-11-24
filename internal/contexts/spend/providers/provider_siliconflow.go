package providers

func providerSiliconFlow() Descriptor {
	return Descriptor{
		ID:          "siliconflow",
		DisplayName: "SiliconFlow",
		// CodexBar: Sources/CodexBarCore/Resources/Plugins/siliconflow.ts and
		// siliconflow-web.js, Providers/SiliconFlow, docs/siliconflow.md
		// (a26ca57b). The China origin's balance API was retired on
		// 2026-08-14 (docs.siliconflow.cn release notes), so a China account
		// is read from its console session, in CNY. Neither is verified
		// against a live account.
		Sources: []Source{
			{Name: "siliconflow_account", Tag: "siliconflow-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /v1/user/info` (api.siliconflow.com)", Shows: "an international account's USD balance"},
			{Name: "siliconflow_web", Tag: "siliconflow-web", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "cloud.siliconflow.cn", Names: []string{"__SF_auth.session-token*"},
					Proof: []string{"__SF_auth.session-token*"}},
				Endpoint: "`GET /walletd-server/api/v1/subject/profile/peek` (cloud.siliconflow.cn console)",
				Shows:    "a China account's console balance, in CNY"},
		},
		Endpoints: []Endpoint{
			{Host: "api.siliconflow.com", Billing: Direct, Source: "https://docs.siliconflow.com/en/api-reference/userinfo/get-user-info"},
			{Host: "api.siliconflow.cn", Billing: Direct, Source: "https://docs.siliconflow.cn"},
		},
		// A China key reads no balance (its API was retired); it is
		// attributed and priced, never sent to the international reader.
		Opencode:  []OpencodeID{{ID: "siliconflow", Endpoint: "siliconflow"}, {ID: "siliconflow-cn", Endpoint: "siliconflow-cn"}},
		EnvVars:   []string{"SILICONFLOW_API_KEY"},
		ModelsDev: []string{"siliconflow", "siliconflow-cn"},
	}
}
