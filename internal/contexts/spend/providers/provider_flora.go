package providers

func providerFlora() Descriptor {
	return Descriptor{
		ID:          "flora",
		DisplayName: "FLORA",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/flora.js,
			// Providers/Flora, docs/flora.md (a26ca57b); FLORA's workspace
			// usage API (developer.flora.ai). Not verified against a live
			// account.
			{Name: "flora_workspace", Tag: "flora-workspace", Kind: Balance, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "WORKSPACE_ID:API_KEY (a key with billing permission on the workspace)",
				Endpoint:  "`GET /api/v1/workspaces/{id}/usage` (app.flora.ai)",
				Shows:     "the workspace's (or your own) available balance, and this month's spend"},
		},
		Docs: Docs{Setup: "`tokenops vendor-usage setup flora` with the workspace ID (ws_…, from its URL) and an API key with billing permission, as WORKSPACE_ID:API_KEY"},
	}
}
