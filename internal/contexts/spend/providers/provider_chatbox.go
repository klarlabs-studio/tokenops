package providers

func providerChatbox() Descriptor {
	return Descriptor{
		ID:          "chatbox",
		DisplayName: "Chatbox AI",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/chatbox.ts,
			// Providers/Chatbox, docs/chatbox.md (a26ca57b), following
			// Chatbox's public client (github.com/chatboxai/chatbox). Not
			// verified against a live account.
			{Name: "chatbox_license", Tag: "chatbox-license", Kind: Subscription, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				EnvVars:   []string{"CHATBOX_LICENSE_KEY"},
				KeyFormat: "a Chatbox AI licence key (it can also authorise AI requests; only the quota is read)",
				Endpoint:  "`GET /api/license/detail/realtime` (api.chatboxai.app)",
				Shows:     "the licence's plan quota, to its next refresh, and its top-up credits"},
		},
	}
}
