package providers

import "time"

func providerDoubao() Descriptor {
	return Descriptor{
		ID:          "doubao",
		DisplayName: "Doubao",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Providers/Doubao: DoubaoUsageFetcher.swift,
			// DoubaoVolcengineSigner.swift, docs/doubao.md) and its fixtures;
			// not verified against a live account. CodexBar's Ark API-key
			// probe is not ported: it sends a chat completion, which spends
			// tokens.
			{Name: "doubao_cli", Tag: "doubao-cli", Kind: Subscription, Credential: CLI,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Doubao/DoubaoUsageFetcher.swift (docs/doubao.md)",
				Command: &Command{Binary: "arkcli", PathEnv: "ARKCLI_PATH",
					Args:      [][]string{{"usage", "plan", "--format", "json"}},
					Timeout:   15 * time.Second,
					SignedOut: []string{"not logged in", "not authenticated", "login required", "arkcli auth login"}},
				Endpoint: "`arkcli usage plan --format json`",
				Shows:    "the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan), from the signed-in arkcli"},
			{Name: "doubao_account", Tag: "doubao-account", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				KeyFormat: "ACCESS_KEY_ID:SECRET_ACCESS_KEY",
				Endpoint:  "`POST /?Action=GetCodingPlanUsage`", Shows: "the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan)"},
		},
		// No Endpoints, opencode IDs or environment variables: the keys a
		// harness sends Ark are inference keys, which cannot read plan
		// usage, and sending them here would only be refused.
		Docs: Docs{Setup: "`tokenops vendor-usage setup doubao` with a Volcengine AccessKey pair as ACCESS_KEY_ID:SECRET_ACCESS_KEY (optionally :REGION)"},
	}
}
