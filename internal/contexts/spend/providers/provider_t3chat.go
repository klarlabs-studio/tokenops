package providers

func providerT3Chat() Descriptor {
	return Descriptor{
		ID:          "t3chat",
		DisplayName: "T3 Chat",
		Sources: []Source{
			// CodexBar: Sources/CodexBarCore/Resources/Plugins/t3chat.js,
			// Providers/T3Chat, docs/t3chat.md. T3 Chat has no usage API;
			// its web app's tRPC call is read with the session. CodexBar's
			// importer names no session cookie to prove a signed-in browser
			// by, so the Cookie header is pasted (PasteOnly).
			{Name: "t3chat_account", Tag: "t3chat-account", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Cookie:   &Cookie{Host: "t3.chat", PasteOnly: true},
				Endpoint: "`GET /api/trpc/getCustomerData`",
				Shows:    "the 4-hour Base window and the monthly Overage budget used"},
		},
	}
}
