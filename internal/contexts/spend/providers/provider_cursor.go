package providers

import "go.klarlabs.de/tokenops/pkg/eventschema"

func providerCursor() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderCursor,
		DisplayName: "Cursor",
		PlanPrefix:  "Cursor ",
		Logo:        true,
		Sources: []Source{
			{Name: "cursor_turns (hook ledger)", Tag: "cursor-hook", Kind: LocalLog, Credential: LocalFile,
				Switch: SwitchAlways, Reader: BespokeReader, Verified: FromDocs,
				Package: "internal/contexts/spend/vendorusage/cursorturns",
				Fixture: "internal/contexts/spend/vendorusage/cursorturns/poller_test.go",
				Shows:   "per-turn consumption Cursor's stop hook records"},
			{Name: "cursor_web", Tag: "cursor-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: FromClientSource,
				Package: "internal/contexts/spend/vendorusage/cursor",
				Fixture: "internal/contexts/spend/vendorusage/cursor/cursor_test.go",
				Shows:   "requests used against the month's allowance, from cursor.com"},
		},
		Docs: Docs{Setup: "`tokenops hooks install --client cursor --coach` records turns; `vendor_usage.cursor` takes the cursor.com session"},
		Plans: []Plan{
			{
				Name:             "cursor-pro",
				Display:          "Cursor Pro",
				RequestsPerMonth: 500,
				RateLimitWindow:  0,
				SourceURL:        "https://docs.cursor.com/account/plans-and-usage (2026-05)",
				MonthlyUSD:       20,
				PriceSource:      "https://cursor.com/pricing (2026-09-30): Individual $20 / mo.",
			},
			{
				Name:             "cursor-business",
				Display:          "Cursor Business",
				RequestsPerMonth: 500,
				RateLimitWindow:  0,
				SourceURL:        "https://docs.cursor.com/account/plans-and-usage (2026-05)",
				MonthlyUSD:       40,
				PerSeat:          true,
				PriceSource:      "https://cursor.com/pricing (2026-09-30): Teams $40 / user / mo.",
			},
		},
	}
}
