package providers

import (
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func providerAnthropic() Descriptor {
	return Descriptor{
		ID:          eventschema.ProviderAnthropic,
		DisplayName: "Claude",
		PlanPrefix:  "Claude ",
		Logo:        true,
		Sources: []Source{
			{Name: "claude_code_statusline", Tag: "claude-code-statusline", Kind: Subscription, Credential: LocalFile,
				Switch: SwitchAlways, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/claudestatusline",
				Fixture: "internal/contexts/spend/vendorusage/claudestatusline/poller_test.go",
				Shows:   "the 5-hour and 7-day windows Claude Code gives its status line"},
			{Name: "claude_code_jsonl", Tag: "claude-code-jsonl", Kind: LocalLog, Credential: LocalFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/claudecodejsonl",
				Fixture: "internal/contexts/spend/vendorusage/claudecodejsonl/reader_test.go",
				Shows:   "per-turn tokens from Claude Code's transcripts"},
			{Name: "claude_subscription", Tag: "claude-usage-meter", Kind: Subscription, Credential: BrowserCookie,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/claudeusagemeter",
				Fixture: "internal/contexts/spend/vendorusage/claudeusagemeter/meter_test.go",
				Cookie:  &Cookie{Host: "claude.ai", Names: []string{"sessionKey", "cf_clearance"}},
				Shows:   "Anthropic's own 5-hour and 7-day utilisation"},
			{Name: "claude_code_oauth", Tag: "claude-code-oauth", Kind: Subscription, Credential: OAuthFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: FromDocs,
				Package: "internal/contexts/spend/vendorusage/claudecodeoauth",
				Fixture: "internal/contexts/spend/vendorusage/claudecodeoauth/claudecodeoauth_test.go",
				Shows:   "the plan windows, with Claude Code's own sign-in (opt-in)"},
			{Name: "vendor_usage_anthropic", Tag: "vendor-usage-anthropic", Kind: Spend, Credential: AdminKey,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: FromDocs,
				Package: "internal/contexts/spend/vendorusage/anthropic",
				Fixture: "internal/contexts/spend/vendorusage/anthropic/fetch_test.go",
				Shows:   "the organisation's token usage from the Admin API"},
			{Name: "claude_code_stats_cache (deprecated)", Tag: "claude-code-stats-cache", Kind: LocalLog, Credential: LocalFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: VerifiedLive,
				Package: "internal/contexts/spend/vendorusage/claudecode",
				Fixture: "internal/contexts/spend/vendorusage/claudecode/cache_test.go",
				Shows:   "daily totals from Claude Code's stats cache (deprecated)"},
		},
		Endpoints: []Endpoint{{Host: "api.anthropic.com", Billing: Direct}},
		Opencode:  []OpencodeID{{ID: "anthropic"}},
		Docs: Docs{
			Setup: "`tokenops init` installs the status line; `tokenops vendor-usage setup claude-subscription` connects the claude.ai meter",
		},
		Plans: []Plan{
			{
				Name:        "claude-max-5x",
				Display:     "Claude Max 5x",
				RelativeTo:  "claude-pro",
				Multiplier:  5,
				WindowUnit:  "messages",
				SourceURL:   "https://support.claude.com/en/articles/11049741-what-is-the-max-plan (2026-09): \"five times the Pro plan's per-session usage allowance\"",
				MonthlyUSD:  100,
				PriceSource: "https://support.claude.com/en/articles/11049741 (2026-09-30): Max 5x $100 per month",
			},
			{
				Name:        "claude-max-20x",
				Display:     "Claude Max 20x",
				RelativeTo:  "claude-pro",
				Multiplier:  20,
				WindowUnit:  "messages",
				SourceURL:   "https://support.claude.com/en/articles/11049741-what-is-the-max-plan (2026-09): \"20 times the Pro plan's per-session usage allowance\"",
				MonthlyUSD:  200,
				PriceSource: "https://support.claude.com/en/articles/11049741 (2026-09-30): Max 20x $200 per month",
			},
			{
				Name:             "claude-enterprise",
				Display:          "Claude Enterprise (usage-based)",
				SpendDenominated: true,
				SourceURL:        "https://support.claude.com/en/articles/12005970-manage-usage-credits-for-team-and-seat-based-enterprise-plans (2026-09): \"all usage is billed at API rates from the first token\"",
			},
			{
				Name:        "claude-team-standard",
				Display:     "Claude Team (Standard seat)",
				RelativeTo:  "claude-pro",
				Multiplier:  1.25,
				WindowUnit:  "messages",
				SourceURL:   "https://support.claude.com/en/articles/9266767-what-is-the-team-plan (2026-09): \"1.25x the Pro plan's per-session usage allowance\"",
				MonthlyUSD:  25,
				PerSeat:     true,
				PriceSource: "https://support.claude.com/en/articles/9266767 (2026-09-30): $25 per member per month, billed monthly",
			},
			{
				Name:        "claude-team-premium",
				Display:     "Claude Team (Premium seat)",
				RelativeTo:  "claude-pro",
				Multiplier:  6.25,
				WindowUnit:  "messages",
				SourceURL:   "https://support.claude.com/en/articles/9266767-what-is-the-team-plan (2026-09): \"6.25x the Pro plan's per-session usage allowance\"",
				MonthlyUSD:  125,
				PerSeat:     true,
				PriceSource: "https://support.claude.com/en/articles/9266767 (2026-09-30): $125 per member per month, billed monthly",
			},
			{
				Name:              "claude-pro",
				Display:           "Claude Pro",
				RateLimitWindow:   5 * time.Hour,
				MessagesPerWindow: 45,
				WindowUnit:        "messages",
				// THE baseline. Every Anthropic tier derives from this one number,
				// so it is the only one that can go stale — and the vendor no
				// longer publishes an absolute to check it against, which is
				// exactly why it is pinned in one place instead of five.
				SourceURL:   "https://support.anthropic.com/en/articles/8325612 (2026-05; vendor no longer publishes an absolute)",
				MonthlyUSD:  20,
				PriceSource: "https://claude.com/pricing (2026-09-30): $20 billed monthly",
			},
		},
	}
}
