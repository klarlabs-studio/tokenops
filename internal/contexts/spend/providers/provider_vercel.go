package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerVercel() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderVercel,
		DisplayName: "Vercel",
		Sources: []Source{
			{Name: "vercel_account", Tag: "vercel-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromDocs,
				Endpoint: "`GET /v1/credits`", Shows: "the team's credit balance"},
		},
		Endpoints: []Endpoint{{Host: "ai-gateway.vercel.sh", Billing: Reseller, Source: "https://vercel.com/docs/ai-gateway"}},
		Opencode:  []OpencodeID{{ID: "vercel", Endpoint: "vercel"}},
		EnvVars:   []string{"AI_GATEWAY_API_KEY"},
		Docs:      Docs{Label: "Vercel AI Gateway"},
	}
}
