// Package plans catalogs the flat-rate LLM subscriptions TokenOps
// tracks alongside metered per-token cost. Each entry pairs a plan
// identifier with the publicly documented monthly quotas so the spend
// engine can surface headroom alongside dollar spend.
//
// The catalog is intentionally a small Go map rather than an external
// file: vendor pricing pages change, and pinning the numbers in source
// (with a sourceURL comment per entry) makes drift visible in PR
// review rather than silent runtime mismatch.
package plans

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Plan describes a single flat-rate subscription. Quotas are monthly
// caps; zero means "no published cap" (rate-limited only). The provider
// field matches the eventschema Provider value emitted on associated
// PromptEvents.
type Plan struct {
	// Name is the catalog identifier used in config (e.g. "claude-max-20x").
	Name string
	// Provider matches eventschema.Provider so the spend engine can
	// route events to the right plan record.
	Provider string
	// Display is the human-readable plan name (e.g. "Claude Max").
	Display string
	// InputTokensPerMonth is the published monthly cap on input tokens.
	// Zero indicates the vendor publishes no fixed cap (rate-limit only).
	InputTokensPerMonth int64
	// OutputTokensPerMonth is the published monthly cap on output
	// tokens. Zero matches InputTokensPerMonth semantics.
	OutputTokensPerMonth int64
	// RequestsPerMonth is the cap on total requests, when published.
	RequestsPerMonth int64
	// RateLimitWindow is the shortest documented rate-limit window
	// (e.g. messages per 5 hours). Used by the headroom calculator to
	// warn before the window resets.
	RateLimitWindow time.Duration
	// MessagesPerWindow is the documented cap on user-facing units
	// (messages or premium requests) within RateLimitWindow. Zero
	// indicates the vendor publishes no concrete number (e.g.
	// "depends on conversation length"); headroom math then surfaces
	// raw consumption without a percentage.
	MessagesPerWindow int64
	// WindowUnit names the user-facing unit MessagesPerWindow counts
	// — "messages", "requests", or "premium_requests". Display only;
	// the consumption reader always counts whole PromptEvents.
	WindowUnit string
	// RelativeTo names the plan this tier's allowance is defined against,
	// and Multiplier is how many times that plan's per-window allowance it
	// receives.
	//
	// Anthropic documents Max and Team only this way — "five times the Pro
	// plan's per-session usage allowance" — and no longer publishes an
	// absolute for any tier, Pro included. Holding three independent
	// absolutes meant they could drift out of the relationship the vendor
	// actually states, and they had: Pro 45 against Max 5x 50 is 1.1x
	// where the documentation says 5x, with both Max entries citing a
	// support URL that now 404s.
	//
	// Deriving leaves exactly one number that can go stale, and correcting
	// it corrects every tier at once. A relative entry carries no
	// MessagesPerWindow of its own; Lookup computes it.
	RelativeTo string
	Multiplier float64
	// SpendDenominated marks a plan whose limit is money, not a
	// rate-limit window.
	//
	// Usage-based Enterprise is billed at API rates from the first token:
	// there is no cap to be under, so there is no percentage to report and
	// no window to have headroom in. What it has instead is a spend limit
	// the org's admins set in the vendor console — a number the operator
	// knows and this catalog never could. So the plan declares that its
	// denominator is supplied, and binding it without one is refused
	// rather than defaulted.
	SpendDenominated bool
	// SourceURL pins the vendor page that documents these limits. Drift
	// surfaces in PR review when the URL or numbers change.
	SourceURL string
}

// catalog is the authoritative plan list. Numbers reflect the public
// vendor documentation snapshot taken on the date in each SourceURL
// comment; bumps require a PR with refreshed URLs.
var catalog = map[string]Plan{
	"claude-max-5x": {
		Name:       "claude-max-5x",
		Provider:   "anthropic",
		Display:    "Claude Max 5x",
		RelativeTo: "claude-pro",
		Multiplier: 5,
		WindowUnit: "messages",
		SourceURL:  "https://support.claude.com/en/articles/11049741-what-is-the-max-plan (2026-09): \"five times the Pro plan's per-session usage allowance\"",
	},
	"claude-max-20x": {
		Name:       "claude-max-20x",
		Provider:   "anthropic",
		Display:    "Claude Max 20x",
		RelativeTo: "claude-pro",
		Multiplier: 20,
		WindowUnit: "messages",
		SourceURL:  "https://support.claude.com/en/articles/11049741-what-is-the-max-plan (2026-09): \"20 times the Pro plan's per-session usage allowance\"",
	},
	"claude-enterprise": {
		Name:             "claude-enterprise",
		Provider:         "anthropic",
		Display:          "Claude Enterprise (usage-based)",
		SpendDenominated: true,
		SourceURL:        "https://support.claude.com/en/articles/12005970-manage-usage-credits-for-team-and-seat-based-enterprise-plans (2026-09): \"all usage is billed at API rates from the first token\"",
	},
	"claude-team-standard": {
		Name:       "claude-team-standard",
		Provider:   "anthropic",
		Display:    "Claude Team (Standard seat)",
		RelativeTo: "claude-pro",
		Multiplier: 1.25,
		WindowUnit: "messages",
		SourceURL:  "https://support.claude.com/en/articles/9266767-what-is-the-team-plan (2026-09): \"1.25x the Pro plan's per-session usage allowance\"",
	},
	"claude-team-premium": {
		Name:       "claude-team-premium",
		Provider:   "anthropic",
		Display:    "Claude Team (Premium seat)",
		RelativeTo: "claude-pro",
		Multiplier: 6.25,
		WindowUnit: "messages",
		SourceURL:  "https://support.claude.com/en/articles/9266767-what-is-the-team-plan (2026-09): \"6.25x the Pro plan's per-session usage allowance\"",
	},
	"claude-pro": {
		Name:              "claude-pro",
		Provider:          "anthropic",
		Display:           "Claude Pro",
		RateLimitWindow:   5 * time.Hour,
		MessagesPerWindow: 45,
		WindowUnit:        "messages",
		// THE baseline. Every Anthropic tier derives from this one number,
		// so it is the only one that can go stale — and the vendor no
		// longer publishes an absolute to check it against, which is
		// exactly why it is pinned in one place instead of five.
		SourceURL: "https://support.anthropic.com/en/articles/8325612 (2026-05; vendor no longer publishes an absolute)",
	},
	"gpt-plus": {
		Name:            "gpt-plus",
		Provider:        "openai",
		Display:         "ChatGPT Plus",
		RateLimitWindow: 5 * time.Hour,
		WindowUnit:      "messages",
		SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): local-message estimates are model-dependent per five-hour window",
	},
	"gpt-pro": {
		Name:            "gpt-pro",
		Provider:        "openai",
		Display:         "ChatGPT Pro (tier unspecified)",
		RateLimitWindow: 5 * time.Hour,
		WindowUnit:      "messages",
		SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): Pro is offered in 5x and 20x tiers",
	},
	"gpt-pro-5x": {
		Name:            "gpt-pro-5x",
		Provider:        "openai",
		Display:         "ChatGPT Pro 5x",
		RateLimitWindow: 5 * time.Hour,
		WindowUnit:      "messages",
		SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): five times Plus Codex usage",
	},
	"gpt-pro-20x": {
		Name:            "gpt-pro-20x",
		Provider:        "openai",
		Display:         "ChatGPT Pro 20x",
		RateLimitWindow: 5 * time.Hour,
		WindowUnit:      "messages",
		SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): 20 times Plus Codex usage",
	},
	"gpt-business": {
		Name:            "gpt-business",
		Provider:        "openai",
		Display:         "ChatGPT Business",
		RateLimitWindow: 5 * time.Hour,
		WindowUnit:      "messages",
		SourceURL:       "https://developers.openai.com/docs/pricing (2026-09): Standard Business is the current workspace plan name",
	},
	"copilot-individual": {
		Name:             "copilot-individual",
		Provider:         "github",
		Display:          "GitHub Copilot Individual",
		RequestsPerMonth: 0,
		RateLimitWindow:  0,
		SourceURL:        "https://docs.github.com/en/copilot/about-github-copilot/plans-for-github-copilot (2026-05)",
	},
	"copilot-business": {
		Name:             "copilot-business",
		Provider:         "github",
		Display:          "GitHub Copilot Business",
		RequestsPerMonth: 0,
		RateLimitWindow:  0,
		SourceURL:        "https://docs.github.com/en/copilot/about-github-copilot/plans-for-github-copilot (2026-05)",
	},
	"cursor-pro": {
		Name:             "cursor-pro",
		Provider:         "cursor",
		Display:          "Cursor Pro",
		RequestsPerMonth: 500,
		RateLimitWindow:  0,
		SourceURL:        "https://docs.cursor.com/account/plans-and-usage (2026-05)",
	},
	"cursor-business": {
		Name:             "cursor-business",
		Provider:         "cursor",
		Display:          "Cursor Business",
		RequestsPerMonth: 500,
		RateLimitWindow:  0,
		SourceURL:        "https://docs.cursor.com/account/plans-and-usage (2026-05)",
	},
	// Google One AI Premium (Gemini Advanced) — fixed monthly
	// subscription with no published message or token caps; Google
	// throttles dynamically. Modeled without a window so headroom math
	// reports consumption trends instead of a cap (mirrors gpt-pro).
	"gemini-ai-premium": {
		Name:      "gemini-ai-premium",
		Provider:  "gemini",
		Display:   "Google One AI Premium",
		SourceURL: "https://one.google.com/about/ai-premium (2026-05)",
	},
	// Mistral Le Chat Pro — fixed monthly subscription, daily message
	// cap published in 2025-Q4. Window unit is "messages per day";
	// modeled as a 24h rolling window for headroom math parity with
	// the other consumer plans.
	"mistral-le-chat-pro": {
		Name:              "mistral-le-chat-pro",
		Provider:          "mistral",
		Display:           "Mistral Le Chat Pro",
		RateLimitWindow:   24 * time.Hour,
		MessagesPerWindow: 200,
		WindowUnit:        "messages",
		SourceURL:         "https://mistral.ai/pricing (2026-05)",
	},
}

// deprecatedAliases maps retired catalog names to the modern entry
// they should resolve to. Lookup follows the alias and ResolveAlias
// surfaces the rename so CLI verbs print a deprecation notice instead
// of an error. Stale docs / blog posts from before the v0.6.0 catalog
// split keep working.
var deprecatedAliases = map[string]string{
	"claude-max":      "claude-max-20x",
	"claude-code-pro": "claude-pro",
	"codex-plus":      "gpt-plus",
	"gpt-team":        "gpt-business",
}

// ResolveAlias returns the modern catalog name when `name` is a known
// deprecation, the input string unchanged otherwise. The second return
// is true only when an alias was applied; callers use it to render a
// "renamed X -> Y" notice.
func ResolveAlias(name string) (string, bool) {
	if modern, ok := deprecatedAliases[name]; ok {
		return modern, true
	}
	return name, false
}

// Lookup returns the catalog entry for name. ok is false when no such
// plan is registered — callers should surface the list of valid names
// (via Names()) so configuration errors are actionable. Deprecated
// aliases are transparently resolved.
func Lookup(name string) (Plan, bool) {
	if modern, aliased := ResolveAlias(name); aliased {
		name = modern
	}
	p, ok := catalog[name]
	if !ok {
		return p, false
	}
	return resolveRelative(p), true
}

// resolveRelative fills a derived tier's per-window allowance from the plan
// it is defined against. A non-derived plan passes through untouched.
//
// A missing base leaves MessagesPerWindow at zero, which the headroom
// calculator already treats as "no published number" and renders as raw
// consumption without a percentage. That is the right failure: a tier whose
// baseline went missing should stop claiming a denominator, not invent one.
func resolveRelative(p Plan) Plan {
	if p.RelativeTo == "" || p.Multiplier <= 0 {
		return p
	}
	base, ok := catalog[p.RelativeTo]
	if !ok {
		return p
	}
	p.MessagesPerWindow = int64(float64(base.MessagesPerWindow)*p.Multiplier + 0.5)
	if p.RateLimitWindow == 0 {
		p.RateLimitWindow = base.RateLimitWindow
	}
	if p.WindowUnit == "" {
		p.WindowUnit = base.WindowUnit
	}
	return p
}

// Names returns the catalog keys sorted lexicographically. Used by
// config validation error messages and the `tokenops plan list` CLI
// surface.
func Names() []string {
	names := make([]string, 0, len(catalog))
	for k := range catalog {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Validate returns nil when name is registered and a descriptive error
// listing valid alternatives otherwise. Config validation calls this so
// a typo in plans.yaml fails Validate() instead of silently falling
// through to metered cost. Deprecated aliases pass validation; callers
// who want the rename hint should call ResolveAlias directly.
func Validate(name string) error {
	if _, ok := catalog[name]; ok {
		return nil
	}
	if _, aliased := ResolveAlias(name); aliased {
		return nil
	}
	if hint := unmodelledPlanHint(name); hint != "" {
		return fmt.Errorf("unknown plan %q: %s", name, hint)
	}
	return fmt.Errorf("unknown plan %q; valid plans: %v", name, Names())
}

// unmodelledPlanHint explains the tiers that are deliberately absent, rather
// than leaving an operator to read their own plan's absence off a list.
//
// Enterprise is not a rate-limit window in either of its forms. Usage-based
// Enterprise bills at API rates from the first token, and seat-based
// Enterprise gives an included per-seat allowance and then bills the
// overflow at API rates. Inventing a window for either would produce
// headroom maths that looks authoritative and is fiction.
func unmodelledPlanHint(name string) string {
	switch strings.ToLower(name) {
	case "claude-team", "team":
		return "Anthropic Team seats are two plans, and they differ by five times — use " +
			"claude-team-standard (1.25x Pro) or claude-team-premium (6.25x Pro)"
	case "claude-enterprise-seats", "enterprise-seats":
		return "seat-based Enterprise is an included per-seat allowance plus metered " +
			"overflow — two denominators at once — and is not modelled yet. For the " +
			"usage-based plan use claude-enterprise with --spend-limit"
	}
	return ""
}

// ValidateSpendLimit checks that a spend-denominated plan has a limit to be
// measured against: one the operator supplies, or one the vendor reports.
//
// The operator's limit lives in config rather than the catalog because it
// is per-org, negotiated, and changeable from a console this tool cannot
// see. Refusing a binding with neither is the point: a spend-denominated
// plan with a defaulted limit would report a percentage against a number
// nobody chose, which reads exactly as authoritative as a real one.
// vendorReportsLimit is true when a vendor meter that reports the limit
// (the Claude usage meter, for Anthropic) is enabled.
func ValidateSpendLimit(name string, limitUSD float64, vendorReportsLimit bool) error {
	p, ok := Lookup(name)
	if !ok || !p.SpendDenominated {
		return nil
	}
	if limitUSD > 0 || vendorReportsLimit {
		return nil
	}
	return fmt.Errorf("plan %q is billed at API rates and has no usage window, so headroom is measured "+
		"against your spend limit. Either let Anthropic report it — `tokenops vendor-usage setup "+
		"claude-usage-meter` — or give it yourself: --spend-limit, or spend_limit_usd from an agent (the figure your admins configured)", name)
}
