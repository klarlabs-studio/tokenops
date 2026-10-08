package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerPerplexity() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderPerplexity,
		DisplayName: "Perplexity",
		Logo:        true,
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/perplexity.js,
			// Providers/Perplexity, docs/perplexity.md. Perplexity's API keys
			// do not read the account's credits; the web session does.
			{Name: "perplexity_account", Tag: "perplexity-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie: &Cookie{Host: "www.perplexity.ai", Names: []string{
					"__Secure-next-auth.session-token", "__Secure-authjs.session-token",
					"next-auth.session-token", "authjs.session-token",
				}},
				Endpoint: "`GET /rest/billing/credits`",
				Shows:    "the plan's monthly credit grant used, and the credit balance"},
		},
		EnvVars: []string{"PERPLEXITY_SESSION_TOKEN", "PERPLEXITY_COOKIE"},
	}
}
