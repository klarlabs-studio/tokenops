package providers

func providerParallel() Descriptor {
	return Descriptor{
		ID:          "parallel",
		DisplayName: "Parallel",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/parallel.ts,
			// Providers/Parallel, docs/parallel.md (a26ca57b). An undocumented
			// dashboard endpoint, read with the browser session: Parallel's
			// API keys cannot read the balance, and its Account API needs an
			// OAuth sign-in. Not verified against a live account.
			{Name: "parallel_web", Tag: "parallel-web", Kind: Balance, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "platform.parallel.ai", AllForHost: true},
				Endpoint: "`POST /api/rpc/billing/payments/refresh-balance` (platform.parallel.ai)",
				Shows:    "the account's USD balance"},
		},
		Endpoints: []Endpoint{{Host: "api.parallel.ai", Billing: Direct, Source: "https://docs.parallel.ai"}},
	}
}
