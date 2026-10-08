package providers

import "time"

// aliyunConsoleCookies are the Alibaba Cloud console sign-in cookies, as
// CodexBar's OneConsole importer recognises a session
// (Sources/CodexBarCore/Providers/Alibaba/AlibabaCodingPlanCookieImporter.swift):
// the ticket and an account marker, with the CSRF and anonymous-ID cookies
// the console's requests carry.
var aliyunConsoleCookies = []string{
	"login_aliyunid_ticket", "login_aliyunid_pk", "login_current_pk", "login_aliyunid",
	"login_aliyunid_csrf", "login_aliyunid_sc", "aliyun_choice", "aliyun_site", "aliyun_lang", "cna", "sec_token",
}

func providerAlibaba() Descriptor {
	return Descriptor{
		ID:          "alibaba",
		DisplayName: "Alibaba Cloud",
		Logo:        true,
		// Ported from CodexBar's Alibaba Coding Plan provider
		// (Sources/CodexBarCore/Providers/Alibaba/AlibabaCodingPlanUsageFetcher.swift).
		Sources: []Source{
			{Name: "alibaba_account", Tag: "alibaba-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`POST /data/api.json` (queryCodingPlanInstanceInfoV2, Model Studio or Bailian console)",
				Shows:    "Coding Plan 5-hour, weekly and monthly quotas (where the console accepts the plan's API key)"},
			{Name: "alibaba_web", Tag: "alibaba-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "modelstudio.console.alibabacloud.com", Also: []string{"bailian.console.aliyun.com"},
					Names: aliyunConsoleCookies, Proof: []string{"login_aliyunid_ticket"}},
				Endpoint: "`POST /data/api.json` (console gateway, with the console's sec_token)",
				Shows:    "Coding Plan 5-hour, weekly and monthly quotas"},
		},
		Endpoints: []Endpoint{{Host: "coding-intl.dashscope.aliyuncs.com", Billing: Reseller,
			Source: "https://www.alibabacloud.com/help/en/model-studio/coding-plan"}},
		Opencode: []OpencodeID{
			{ID: "alibaba-coding-plan", Endpoint: "alibaba"},
			{ID: "alibaba-coding-plan-cn", Endpoint: "alibaba"},
			{ID: "alibaba", Endpoint: "alibaba-api"},
			{ID: "alibaba-cn", Endpoint: "alibaba-api"},
		},
		EnvVars:   []string{"ALIBABA_CODING_PLAN_API_KEY", "ALIBABA_QWEN_API_KEY", "DASHSCOPE_API_KEY", "ALIBABA_CODING_PLAN_COOKIE"},
		ModelsDev: []string{"alibaba"},
		Docs:      Docs{Label: "Alibaba Cloud Coding Plan", CatalogLabel: "Alibaba Coding"},
		Plans: []Plan{
			{
				Name: "alibaba-coding-pro", Display: "Alibaba Cloud Coding Plan Pro",
				RateLimitWindow: 5 * time.Hour, MessagesPerWindow: 6000, WindowUnit: "requests",
				SourceURL:   "https://www.alibabacloud.com/help/en/model-studio/coding-plan (2026-10-01): 6,000 requests per 5 hours, 45,000 per week, 90,000 per month",
				MonthlyUSD:  50,
				PriceSource: "https://www.alibabacloud.com/help/en/model-studio/coding-plan (2026-10-01): Pro $50 per month",
			},
		},
	}
}
