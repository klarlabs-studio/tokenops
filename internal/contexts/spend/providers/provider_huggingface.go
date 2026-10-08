package providers

func providerHuggingFace() Descriptor {
	return Descriptor{
		ID:          "huggingface",
		DisplayName: "Hugging Face",
		Logo:        true,
		Sources: []Source{
			// Built from CodexBar's source (steipete/CodexBar
			// Sources/CodexBarCore/Resources/Plugins/huggingface.ts,
			// Providers/HuggingFace, docs/huggingface.md) and its fixtures:
			// the billing endpoint is in Hugging Face's OpenAPI spec, its
			// answer is not documented. Not verified against a live account.
			// Not ported: the ZeroGPU quota (Spaces GPU time, not inference
			// spend), the prepaid wallet scraped from the billing page with a
			// browser session, and the token file `hf auth login` writes.
			{Name: "huggingface_account", Tag: "huggingface-account", Kind: Spend, Credential: APIKey,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Endpoint: "`GET /api/settings/billing/usage-v2`", Shows: "Inference Providers charges this month, against the spending limit when set"},
		},
		Endpoints: []Endpoint{{Host: "router.huggingface.co", Billing: Reseller, Source: "https://huggingface.co/docs/inference-providers/pricing"}},
		Opencode:  []OpencodeID{{ID: "huggingface", Endpoint: "huggingface"}},
		EnvVars:   []string{"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN"},
		ModelsDev: []string{"huggingface"},
	}
}
