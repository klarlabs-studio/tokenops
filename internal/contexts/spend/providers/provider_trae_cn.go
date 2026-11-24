package providers

func providerTraeCN() Descriptor {
	return Descriptor{
		ID:          "trae-cn",
		DisplayName: "Trae CN",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/traecn.js,
			// Providers/TraeCN, docs/traecn.md (a26ca57b). ByteDance's
			// China-region Trae; a reverse-engineered console API read with
			// the trae.cn browser session. Not verified against a live
			// account.
			{Name: "trae_cn_web", Tag: "trae-cn-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "api.trae.cn", Also: []string{"www.trae.cn"}, AllForHost: true},
				Endpoint: "`POST /cloudide/api/v3/common/GetUserToken`, `POST /trae/api/v2/pay/user_current_entitlement_list` (api.trae.cn)",
				Shows:    "the credits used across the account's packs (packs expire on their own: no reset)"},
		},
	}
}
