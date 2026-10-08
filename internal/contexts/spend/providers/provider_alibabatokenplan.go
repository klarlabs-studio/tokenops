package providers

func providerAlibabaTokenPlan() Descriptor {
	return Descriptor{
		ID:          "alibabatokenplan",
		DisplayName: "Alibaba Token Plan",
		Logo:        true,
		// Ported from CodexBar's Alibaba Token Plan provider
		// (Sources/CodexBarCore/Providers/Alibaba/AlibabaTokenPlanUsageFetcher.swift,
		// docs/alibaba-token-plan.md). Its Bailian CLI source (`bl usage
		// token-plan`) is not ported: it needs a poller of its own.
		Sources: []Source{
			{Name: "alibabatokenplan_web", Tag: "alibabatokenplan-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "modelstudio.console.alibabacloud.com", Also: []string{"bailian.console.aliyun.com"},
					Names: aliyunConsoleCookies, Proof: []string{"login_aliyunid_ticket"}},
				Endpoint: "`POST /data/api.json` (tokenplan/personal/api/v2/usage, or GetSubscriptionSummary for a Team plan)",
				Shows:    "Personal/Solo 5-hour, weekly and monthly windows, or a Team plan's credit pool"},
		},
		Docs: Docs{Label: "Alibaba Cloud Token Plan"},
	}
}
