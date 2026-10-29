package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

// creditsSource and plansSource are GitHub's pages for the plans and
// their AI Credits, with the date they were read.
const (
	creditsSource = "https://docs.github.com/en/copilot/concepts/billing/usage-based-billing-for-individuals (2026-10-27)"
	plansSource   = "https://docs.github.com/en/copilot/get-started/plans (2026-10-27)"
)

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
				Shows:   "the month's AI Credits (premium requests on annual plans) against the allowance; an org seat with none says so"},
		},
		Opencode: []OpencodeID{{ID: "github-copilot"}, {ID: "github"}},
		// models.dev lists the models Copilot serves at their vendors'
		// per-token prices: the API-equivalent value of a Copilot turn.
		ModelsDev: []string{"github-copilot"},
		Docs:      Docs{Label: "GitHub Copilot", Setup: "`vendor_usage.github_copilot.enabled: true`; the OAuth token is found where the Copilot IDE plugins (~/.config/github-copilot) or the Copilot CLI (its environment variables, Keychain item or ~/.copilot/config.json) keep it; `use_gh_cli: true` also asks the GitHub CLI"},
		// Since 2026-06-01 every monthly plan bills in GitHub AI Credits
		// (1 credit = 0.01 USD), reset at 00:00 UTC on the 1st. The
		// allowance is in the SourceURL; the vendor's own meter carries
		// the share used, so no count is held here. Annual Pro and Pro+
		// keep premium requests until they expire.
		Plans: []Plan{
			{
				Name:        "copilot-individual",
				Display:     "GitHub Copilot Pro",
				SourceURL:   creditsSource + ": Copilot Pro 1,500 AI credits a month (1,000 base + 500 flex)",
				MonthlyUSD:  10,
				PriceSource: plansSource + ": Copilot Pro $10 USD per month",
			},
			{
				Name:        "copilot-pro-plus",
				Display:     "GitHub Copilot Pro+",
				SourceURL:   creditsSource + ": Copilot Pro+ 7,000 AI credits a month (3,900 base + 3,100 flex)",
				MonthlyUSD:  39,
				PriceSource: plansSource + ": Copilot Pro+ $39 USD per month",
			},
			{
				Name:        "copilot-max",
				Display:     "GitHub Copilot Max",
				SourceURL:   creditsSource + ": Copilot Max 20,000 AI credits a month (10,000 base + 10,000 flex)",
				MonthlyUSD:  100,
				PriceSource: plansSource + ": Copilot Max $100 USD per month",
			},
			{
				Name:        "copilot-business",
				Display:     "GitHub Copilot Business",
				SourceURL:   plansSource + ": Copilot Business 1,900 AI credits per user per month",
				MonthlyUSD:  19,
				PerSeat:     true,
				PriceSource: plansSource + ": $19 USD per granted seat per month",
			},
			{
				Name:        "copilot-enterprise",
				Display:     "GitHub Copilot Enterprise",
				SourceURL:   plansSource + ": Copilot Enterprise 3,900 AI credits per user per month",
				MonthlyUSD:  39,
				PerSeat:     true,
				PriceSource: plansSource + ": $39 USD per granted seat per month",
			},
		},
	}
}
