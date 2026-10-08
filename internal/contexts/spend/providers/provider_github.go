package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerGitHub() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderGitHub,
		DisplayName: "Copilot",
		PlanPrefix:  "GitHub Copilot ",
		Logo:        true,
		Sources: []Source{
			{Name: "github_copilot", Tag: "github-copilot", Kind: Subscription, Credential: OAuthFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: FromClientSource,
				Package: "internal/contexts/spend/vendorusage/copilot",
				Fixture: "internal/contexts/spend/vendorusage/copilot/copilot_test.go",
				Shows:   "premium requests used against the month's allowance"},
		},
		Opencode: []OpencodeID{{ID: "github-copilot"}, {ID: "github"}},
		Docs:     Docs{Label: "GitHub Copilot", Setup: "`vendor_usage.github_copilot.enabled: true`; the OAuth token is found in ~/.config/github-copilot"},
		Plans: []Plan{
			{
				Name:             "copilot-individual",
				Display:          "GitHub Copilot Individual",
				RequestsPerMonth: 0,
				RateLimitWindow:  0,
				SourceURL:        "https://docs.github.com/en/copilot/about-github-copilot/plans-for-github-copilot (2026-05)",
				MonthlyUSD:       10,
				PriceSource:      "https://docs.github.com/copilot/get-started/plans (2026-09-30): Copilot Pro $10 USD per month",
			},
			{
				Name:             "copilot-business",
				Display:          "GitHub Copilot Business",
				RequestsPerMonth: 0,
				RateLimitWindow:  0,
				SourceURL:        "https://docs.github.com/en/copilot/about-github-copilot/plans-for-github-copilot (2026-05)",
				MonthlyUSD:       19,
				PerSeat:          true,
				PriceSource:      "https://docs.github.com/copilot/get-started/plans (2026-09-30): $19 USD per granted seat per month",
			},
		},
	}
}
