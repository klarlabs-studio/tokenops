package providers

func providerDoubao() Descriptor {
	return Descriptor{
		ID:          "doubao",
		DisplayName: "Doubao",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Providers/Doubao: DoubaoUsageFetcher.swift,
			// DoubaoVolcengineSigner.swift, docs/doubao.md) and its fixtures;
			// not verified against a live account. CodexBar's other two
			// sources are not ported: `arkcli usage plan` needs a bespoke CLI
			// poller, and its Ark API-key probe sends a chat completion,
			// which spends tokens.
			{Name: "doubao_account", Tag: "doubao-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromClientSource,
				KeyFormat: "ACCESS_KEY_ID:SECRET_ACCESS_KEY",
				Endpoint:  "`POST /?Action=GetCodingPlanUsage`", Shows: "the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan)"},
		},
		// No Endpoints, opencode IDs or environment variables: the keys a
		// harness sends Ark are inference keys, which cannot read plan
		// usage, and sending them here would only be refused.
		Docs: Docs{Setup: "`tokenops vendor-usage setup doubao` with a Volcengine AccessKey pair as ACCESS_KEY_ID:SECRET_ACCESS_KEY (optionally :REGION)"},
	}
}
