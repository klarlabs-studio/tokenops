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
	"claude-code-max": {
		Name:            "claude-code-max",
		Provider:        "anthropic",
		Display:         "Claude Code (Max plan)",
		RateLimitWindow: 5 * time.Hour,
		SourceURL:       "https://docs.claude.com/en/docs/claude-code/setup#pricing (2026-05)",
	},
	"claude-code-pro": {
		Name:            "claude-code-pro",
		Provider:        "anthropic",
		Display:         "Claude Code (Pro plan)",
		RateLimitWindow: 5 * time.Hour,
		SourceURL:       "https://docs.claude.com/en/docs/claude-code/setup#pricing (2026-05)",
	},
	"gpt-plus": {
		Name:              "gpt-plus",
		Provider:          "openai",
		Display:           "ChatGPT Plus",
		RateLimitWindow:   3 * time.Hour,
		MessagesPerWindow: 80,
		WindowUnit:        "messages",
		SourceURL:         "https://help.openai.com/en/articles/9275245 (2026-05)",
	},
	"gpt-pro": {
		Name:      "gpt-pro",
		Provider:  "openai",
		Display:   "ChatGPT Pro",
		SourceURL: "https://openai.com/chatgpt/pricing (2026-05)",
	},
	"gpt-team": {
		Name:              "gpt-team",
		Provider:          "openai",
		Display:           "ChatGPT Team",
		RateLimitWindow:   3 * time.Hour,
		MessagesPerWindow: 120,
		WindowUnit:        "messages",
		SourceURL:         "https://openai.com/chatgpt/pricing (2026-05)",
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
	// Codex Plus — OpenAI's subscription tier for the codex.com /
	// Codex CLI. Window matches GPT Plus (3h rolling) per OpenAI's
	// shared rate-limit policy across consumer products.
	"codex-plus": {
		Name:              "codex-plus",
		Provider:          "openai",
		Display:           "Codex Plus",
		RateLimitWindow:   3 * time.Hour,
		MessagesPerWindow: 80,
		WindowUnit:        "messages",
		SourceURL:         "https://platform.openai.com/docs/guides/codex (2026-05)",
	},
}

// deprecatedAliases maps retired catalog names to the modern entry
// they should resolve to. Lookup follows the alias and ResolveAlias
// surfaces the rename so CLI verbs print a deprecation notice instead
// of an error. Stale docs / blog posts from before the v0.6.0 catalog
// split keep working.
var deprecatedAliases = map[string]string{
	"claude-max": "claude-max-20x",
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
	case "claude-enterprise", "claude-team", "enterprise", "team":
		return "Anthropic Team seats are two plans — use claude-team-standard or " +
			"claude-team-premium. Enterprise has no plan entry on purpose: usage-based " +
			"Enterprise is billed at API rates from the first token, and seat-based " +
			"Enterprise is an included allowance plus metered overflow, so neither is a " +
			"rate-limit window. Leave the provider unbound and tokenops will cost the " +
			"traffic from the rate card"
	}
	return ""
}
