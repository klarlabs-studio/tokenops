package providers

import "time"

func providerAlibabaTokenPlan() Descriptor {
	return Descriptor{
		ID:          "alibabatokenplan",
		DisplayName: "Alibaba Token Plan",
		Logo:        true,
		// Ported from CodexBar's Alibaba Token Plan provider
		// (Sources/CodexBarCore/Providers/Alibaba/AlibabaTokenPlanUsageFetcher.swift,
		// docs/alibaba-token-plan.md), with its Bailian CLI source first.
		Sources: []Source{
			// The signed-in Bailian CLI signs its own request; it is given
			// only PATH, HOME, locale, time zone and proxy variables, so no
			// key or cookie in TokenOps' environment reaches it. The
			// international console first, then the mainland one.
			{Name: "alibabatokenplan_cli", Tag: "alibabatokenplan-cli", Kind: Subscription, Credential: CLI,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Alibaba/AlibabaTokenPlanCLIUsageFetcher.swift (docs/alibaba-token-plan.md)",
				Command: &Command{Binary: "bl",
					Args: [][]string{
						{"usage", "token-plan", "--console-region", "ap-southeast-1", "--console-site", "international", "--output", "json"},
						{"usage", "token-plan", "--console-region", "cn-beijing", "--console-site", "domestic", "--output", "json"},
					},
					Timeout: 15 * time.Second,
					EnvAllow: []string{"PATH", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
						"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}},
				Endpoint: "`bl usage token-plan --output json`",
				Shows:    "the 5-hour, weekly and monthly windows, from the signed-in Bailian CLI"},
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
