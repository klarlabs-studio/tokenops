package providers

func providerMuseAI() Descriptor {
	return Descriptor{
		ID:          "museai",
		DisplayName: "Muse",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/museai.js
			// (Providers/MuseAI), docs/museai.md. muse.ai is Meta's
			// consumer agent, not Muse Code.
			{Name: "museai_web", Tag: "museai-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "muse.ai", Names: []string{"hatch_sess"}},
				Endpoint: "`POST muse.ai/` (the page's fetchSubscriptionAction server action)",
				Shows:    "the weekly token allowance used"},
		},
		Docs: Docs{Label: "Muse (muse.ai)"},
	}
}
