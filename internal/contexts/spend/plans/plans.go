// Package plans catalogs the flat-rate LLM subscriptions TokenOps
// tracks alongside metered per-token cost. Each entry pairs a plan
// identifier with the publicly documented monthly quotas so the spend
// engine can surface headroom alongside dollar spend.
//
// The catalog is pinned in source rather than an external file: vendor
// pricing pages change, and pinning the numbers (with a SourceURL per
// entry) makes drift visible in PR review rather than silent runtime
// mismatch. Each plan lives in its provider's descriptor
// (internal/contexts/spend/providers); this package reads them from there.
package plans

import (
	"fmt"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// Plan describes a single flat-rate subscription. Quotas are monthly
// caps; zero means "no published cap" (rate-limited only). The provider
// field matches the eventschema Provider value emitted on associated
// PromptEvents. It is declared with the provider registry, which holds
// each provider's plans.
type Plan = providers.Plan

// catalog is the authoritative plan list: every provider descriptor's
// plans, by name. Numbers reflect the public vendor documentation
// snapshot taken on the date in each SourceURL; bumps require a PR with
// refreshed URLs.
var catalog = func() map[string]Plan {
	out := map[string]Plan{}
	for _, p := range providers.Plans() {
		out[p.Name] = p
	}
	return out
}()

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

// PayAsYouGo binds a provider billed per token with no subscription, so
// its spend is measured against a limit the operator sets: a Fireworks or
// OpenRouter account with a monthly cap. It belongs to no provider, so it
// can be bound to any.
const PayAsYouGo = "pay-as-you-go"

var payAsYouGo = Plan{
	Name:             PayAsYouGo,
	Display:          "Pay as you go (spend limit)",
	SpendDenominated: true,
	SourceURL:        "https://docs.fireworks.ai/serverless/pricing (2026-10-01): billed per token; the limit is the account's own",
}

// Subscription binds a provider whose own account reader reports a
// subscription's usage windows, when the catalog has no plan for it or the
// tier is not known: the vendor's windows are the whole answer, and no
// allowance is assumed. `tokenops plan set` names the tier.
const Subscription = "subscription"

var subscription = Plan{
	Name:      Subscription,
	Display:   "Subscription (windows the vendor reports)",
	SourceURL: "the vendor's own account endpoint, read with the key the harness uses",
}

// ForVendorPlanType is the catalog plan a vendor-reported plan type
// names, when exactly one plan of that provider claims it.
func ForVendorPlanType(provider, planType string) (string, bool) {
	planType = strings.ToLower(strings.TrimSpace(planType))
	if planType == "" {
		return "", false
	}
	found := ""
	for name, p := range catalog {
		if p.Provider != provider {
			continue
		}
		for _, t := range p.VendorPlanTypes {
			if t == planType {
				if found != "" {
					return "", false
				}
				found = name
			}
		}
	}
	return found, found != ""
}

// Covers reports whether a plan covers its provider's usage, so that usage
// is recorded as plan-included at no per-request cost. A rate-limited
// subscription does. A spend-denominated plan does not: usage-based
// Enterprise is billed at API rates from the first token, and recording it
// as covered priced real spend at $0. An unknown plan name keeps the old
// behaviour and covers, since a binding the catalog cannot read was still
// meant as a subscription.
func Covers(name string) bool {
	if name == "" {
		return false
	}
	p, ok := Lookup(name)
	return !ok || !p.SpendDenominated
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
	switch name {
	case PayAsYouGo:
		return payAsYouGo, true
	case Subscription:
		return subscription, true
	}
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
	if _, ok := catalog[name]; ok || name == PayAsYouGo {
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
