package providers

import (
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

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
			// Cursor's desktop app keeps its own sign-in in a local database
			// (CodexBar's CursorAppAuth, Verified FromCodexBar): read only
			// once granted (ADR 0013), from a copy of the database, and
			// never refreshed. Cursor 3.27 and later keep the token encrypted;
			// the reader says so and does not decrypt it.
			{Name: "cursor_app", Tag: "cursor-app", Kind: Subscription, Credential: AppLogin,
				Switch: SwitchAccounts, Reader: AccountReader, Verified: FromCodexBar,
				Reference: "CodexBar Sources/CodexBarCore/Providers/Cursor/CursorAppAuth.swift (CursorAppAuthStore, CursorStatusProbe+AppAuth)",
				AppLogins: []AppLoginItem{{App: "Cursor", Kind: AppLoginSQLite,
					Paths:  []string{"~/Library/Application Support/Cursor/User/globalStorage/state.vscdb", "~/.config/Cursor/User/globalStorage/state.vscdb"},
					Query:  "SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken' LIMIT 1",
					Fields: []string{"value"},
					Host:   "cursor.com",
					// Cursor 3.27 and later keep the token sealed under its
					// Electron safe-storage key (CodexBar #4790).
					Unlock: &AppLoginItem{App: "Cursor", Kind: AppLoginKeychain,
						Service: "Cursor Safe Storage", Account: "Cursor Key", Fields: []string{""}},
					Sealed: "cursor-safe-storage:v1:"}},
				Endpoint: "`GET /api/usage-summary`, `POST /api/dashboard/get-sand-usage-status`",
				Shows:    "the weekly pool shared across Cursor and Grok, or the billing cycle's included usage, from cursor.com",
				Connect: &Connect{
					Command: "tokenops vendor-usage setup cursor --use-app-login",
					Needs:   "Cursor's own sign-in on this Mac; asks first",
				}},
			{Name: "cursor_web", Tag: "cursor-web", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: FromCodexBar,
				Reference: "CodexBar docs/cursor.md and Sources/CodexBarCore/Providers/Cursor (usage-summary, get-sand-usage-status)",
				Package:   "internal/contexts/spend/vendorusage/cursor",
				Fixture:   "internal/contexts/spend/vendorusage/cursor/cursor_test.go",
				Shows:     "the weekly pool, or the billing cycle's included usage (legacy: requests), from cursor.com",
				// The session cookie is the way in when Cursor's app sign-in
				// is not granted (cursor_app comes before this one).
				Connect: &Connect{
					Command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
					Needs:   "your cursor.com browser session (the WorkosCursorSessionToken cookie)",
					Where:   "signed in at cursor.com: developer tools → Application → Cookies → WorkosCursorSessionToken",
				}},
		},
		Presence: []Presence{{App: "Cursor.app"}, {Dir: "~/.cursor"}, {Binary: "cursor-agent"}},
		Docs:     Docs{Setup: "`tokenops hooks install --client cursor --coach` records turns; `vendor_usage.cursor` takes the cursor.com session"},
		// The plans Cursor sells, read on cursor.com/pricing and
		// cursor.com/docs/account/pricing on 2026-10-27: a weekly pool
		// shared across Cursor and Grok, given as multiples of Plus, with
		// no absolute amount published. Pro and Teams (Business) are
		// legacy plans that continue for existing customers; their IDs
		// stay so a stored binding keeps working.
		Plans: []Plan{
			cursorWeekly("cursor-plus", "Cursor Plus", 8, "Base usage (1x)"),
			cursorWeekly("cursor-premium", "Cursor Premium", 30, "4x Plus"),
			cursorWeekly("cursor-super", "Cursor Super", 100, "15x Plus"),
			cursorWeekly("cursor-ultra", "Cursor Ultra", 200, "50x Plus"),
			{
				Name:    "cursor-teams",
				Display: "Cursor Teams",
				// A seat costs $30, $100 or $200 by its tier, so the plan
				// carries no single list price; --price records the seat's.
				SourceURL: cursorPricingDocs + ": seats are Premium, Super or Ultra, each with the matching individual plan's weekly usage",
				PerSeat:   true,
			},
			{
				// Request-based until 2025; the usage-summary reader reads
				// what the plan includes now.
				Name:        "cursor-pro",
				Display:     "Cursor Pro (legacy)",
				SourceURL:   cursorPricingDocs + ": legacy, continues for existing customers; no allowance published",
				MonthlyUSD:  20,
				PriceSource: "https://cursor.com/pricing (2026-09-30): Individual $20 / mo.",
			},
			{
				Name:        "cursor-business",
				Display:     "Cursor Teams (legacy)",
				SourceURL:   cursorPricingDocs + ": legacy Teams seats continue for existing customers; no allowance published",
				MonthlyUSD:  40,
				PerSeat:     true,
				PriceSource: "https://cursor.com/pricing (2026-09-30): Teams $40 / user / mo.",
			},
		},
	}
}

const cursorPricingDocs = "https://cursor.com/docs/account/pricing (2026-10-27)"

// cursorWeekly is a current individual plan: a weekly pool, its size
// given only relative to Plus.
func cursorWeekly(name, display string, usd float64, usage string) Plan {
	return Plan{
		Name:            name,
		Display:         display,
		RateLimitWindow: 7 * 24 * time.Hour,
		WindowUnit:      "usage",
		SourceURL:       cursorPricingDocs + ": weekly pool shared across Cursor and Grok, " + usage,
		MonthlyUSD:      usd,
		PriceSource:     cursorPricingDocs + ": $" + strconv.FormatFloat(usd, 'f', -1, 64) + "/mo",
	}
}
