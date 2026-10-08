package providers

func providerBedrock() Descriptor {
	return Descriptor{
		ID:          "bedrock",
		DisplayName: "Amazon Bedrock",
		Logo:        true,
		Sources: []Source{
			// Cost Explorer bills each request, so this reads only after
			// setup opted in, and at most every 8 hours.
			{Name: "bedrock_cost_explorer", Tag: "bedrock-cost-explorer", Kind: Spend, Credential: CredentialChain,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Bedrock/BedrockUsageStats.swift (docs/bedrock.md)",
				Endpoint:  "Cost Explorer `GetCostAndUsage`",
				Shows:     "this month's Bedrock spend, every 8 hours (Cost Explorer bills $0.01 a request)"},
		},
		// opencode's Bedrock turns bill to AWS; models.dev prices them.
		Opencode:  []OpencodeID{{ID: "amazon-bedrock", Endpoint: "bedrock"}},
		ModelsDev: []string{"amazon-bedrock"},
		Docs: Docs{
			Setup: "`tokenops vendor-usage setup bedrock` opts in with the AWS credentials on this machine (`AWS_ACCESS_KEY_ID` or the shared credentials file's `AWS_PROFILE`/default profile; needs `ce:GetCostAndUsage`)",
		},
	}
}
