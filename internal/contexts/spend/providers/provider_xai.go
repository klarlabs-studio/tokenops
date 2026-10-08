package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerXAI() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderXAI,
		DisplayName: "xAI",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/xai.js, Providers/XAI,
			// docs/xai.md) and xAI's Management API reference; not verified
			// against a live account. The Management API takes a management
			// key and the team ID; an inference key is refused, so none of
			// the harnesses' xAI keys is sent here.
			{Name: "xai_account", Tag: "xai-account", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "TEAM_ID:MANAGEMENT_KEY",
				Endpoint:  "`GET /v1/billing/teams/{team_id}/prepaid/balance`, `POST /v1/billing/teams/{team_id}/usage`",
				Shows:     "the team's posted prepaid USD credit, and its spend over the last 30 days"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup xai` with the team ID and a Management API key (console.x.ai → Settings → Management Keys), as TEAM_ID:MANAGEMENT_KEY"},
	}
}
