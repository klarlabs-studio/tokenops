// Package config loads TokenOps daemon configuration from a YAML file with
// environment-variable overrides. The schema is intentionally small at this
// stage; subsequent tasks (proxy-providers, optimizer, observability) extend
// it with their own sections.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Mode values. Passive collects + analyzes on demand (default); Active
// additionally intervenes: the proxy applies routing rules to live
// traffic and the daemon runs the background spend watcher.
const (
	ModePassive = "passive"
	ModeActive  = "active"
)

// Config is the root daemon configuration.
type Config struct {
	// Mode selects how TokenOps helps: "passive" (analytics only —
	// observe, store, answer queries) or "active" (passive + live
	// interventions: routing rules applied to proxied traffic, budget /
	// unpriced-model watcher emitting alerts). Default passive.
	Mode      string            `yaml:"mode"`
	Listen    string            `yaml:"listen"`
	Log       LogConfig         `yaml:"log"`
	Shutdown  ShutdownConfig    `yaml:"shutdown"`
	Providers map[string]string `yaml:"providers"`
	// Plans maps provider name → plan catalog identifier (e.g.
	// "anthropic" → "claude-max-20x"). Requests routed to a provider with
	// a configured plan are billed as plan_included (CostUSD=0) and
	// roll up to the plan's monthly quota instead. See
	// internal/contexts/spend/plans for the catalog.
	Plans map[string]string `yaml:"plans"`
	// PreferredModels maps provider name → the model you want to stay on
	// (e.g. "anthropic" → "claude-opus-5"). It acts as a ceiling: a
	// routing rule that would move you to a pricier model is refused and
	// referred to you instead of applied, with the preferred model
	// offered as the alternative. Routing DOWN to something cheaper
	// still applies automatically. Empty disables the ceiling.
	PreferredModels map[string]string `yaml:"preferred_models,omitempty"`
	TLS             TLSConfig         `yaml:"tls"`
	Storage         StorageConfig     `yaml:"storage"`
	Retention       RetentionConfig   `yaml:"retention,omitempty"`
	OTel            OTelConfig        `yaml:"otel"`
	Rules           RulesConfig       `yaml:"rules"`
	Resilience      ResilienceConfig  `yaml:"resilience"`
	VendorUsage     VendorUsageConfig `yaml:"vendor_usage"`
	MDNS            MDNSConfig        `yaml:"mdns,omitempty"`
	// PlanLimits carries the per-provider figures only the operator can
	// supply, keyed by provider name. Spend-denominated plans
	// (usage-based Enterprise) have no vendor-published cap, so their
	// denominator comes from the org's own console.
	PlanLimits map[string]PlanLimit `yaml:"plan_limits,omitempty"`
	Dashboard  DashboardConfig      `yaml:"dashboard"`
	Pricing    PricingConfig        `yaml:"pricing"`
	Optimizer  OptimizerConfig      `yaml:"optimizer"`
	Coaching   CoachingConfig       `yaml:"coaching"`
	Budgets    []BudgetConfig       `yaml:"budgets"`
	Watch      WatchConfig          `yaml:"watch"`
}

// ActiveMode reports whether interventions (live routing, spend
// watcher) are enabled. Empty Mode means passive.
func (c Config) ActiveMode() bool { return strings.EqualFold(c.Mode, ModeActive) }

// ParseMode normalises an operating mode a caller asked for, refusing
// anything but passive or active. The terminal's `mode` and the MCP tool
// share it, so neither can write a mode the other would refuse.
func ParseMode(s string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(s)); m {
	case ModePassive, ModeActive:
		return m, nil
	default:
		return "", fmt.Errorf("mode must be %q or %q, got %q", ModePassive, ModeActive, s)
	}
}

// PreferredModel returns the operator's ceiling model for a provider, or
// "" when none is configured.
func (c Config) PreferredModel(provider eventschema.Provider) string {
	return c.PreferredModels[string(provider)]
}

// BudgetConfig is one spend limit the watcher (active mode) and
// on-demand evaluations check. Window is a calendar window in UTC.
type BudgetConfig struct {
	Name     string  `yaml:"name"`
	Window   string  `yaml:"window"` // daily | weekly | monthly
	LimitUSD float64 `yaml:"limit_usd"`
	// LimitTokens is the ceiling for a `basis: tokens` budget. Required
	// for that basis and ignored otherwise. Flat-rate plans bill $0.00
	// at the margin, so this is the only limit that can trip for them.
	LimitTokens int64 `yaml:"limit_tokens"`
	// WarnAt / CritAt are fractional thresholds of the active limit;
	// zero falls back to the budget engine defaults (0.75 / 0.95).
	WarnAt float64 `yaml:"warn_at"`
	CritAt float64 `yaml:"crit_at"`
	// WorkflowID / AgentID optionally scope the limit to one workflow
	// or agent; empty applies to all spend.
	WorkflowID string `yaml:"workflow_id"`
	AgentID    string `yaml:"agent_id"`
	// Basis selects what the limit watches: "spend" (default — real
	// billed cost), "equivalent" (API list-price value, including
	// plan-covered usage), or "tokens" (raw token volume, paired with
	// limit_tokens). Flat-plan deployments want "tokens" for a real
	// ceiling, or "equivalent" for a list-price counterfactual — their
	// billed spend is always ~0.
	Basis string `yaml:"basis"`
}

// Validate checks one budget on its own. Config.Validate runs it over every
// budget; UpsertBudget runs it on the merged budget before storing it, so an
// edit is refused with the same rule the daemon would apply at boot.
func (b BudgetConfig) Validate() error {
	if b.Name == "" {
		return errors.New("name is required")
	}
	switch strings.ToLower(b.Window) {
	case string(budget.WindowDaily), string(budget.WindowWeekly), string(budget.WindowMonthly):
	default:
		return fmt.Errorf("window must be daily, weekly, or monthly, got %q", b.Window)
	}
	// The limit is denominated in the basis's own unit: a token
	// budget is configured with limit_tokens, everything else with
	// limit_usd. Requiring the wrong one is how a flat-rate operator
	// ends up budgeting against dollars they are never billed.
	switch basis := strings.ToLower(b.Basis); basis {
	case budget.BasisTokens:
		if b.LimitTokens <= 0 {
			return fmt.Errorf("limit_tokens must be positive for basis %q, got %d",
				budget.BasisTokens, b.LimitTokens)
		}
	case "", budget.BasisSpend, budget.BasisEquivalent:
		if b.LimitUSD <= 0 {
			return fmt.Errorf("limit_usd must be positive, got %g", b.LimitUSD)
		}
	default:
		return fmt.Errorf("basis must be %q, %q, or %q, got %q",
			budget.BasisSpend, budget.BasisEquivalent, budget.BasisTokens, b.Basis)
	}
	if b.WarnAt < 0 || b.WarnAt > 1 || b.CritAt < 0 || b.CritAt > 1 {
		return errors.New("warn_at and crit_at must be in [0,1]")
	}
	return nil
}

// BudgetLimits maps the configured budgets into the budget engine's
// domain type.
func (c Config) BudgetLimits() []budget.Limit {
	if len(c.Budgets) == 0 {
		return nil
	}
	out := make([]budget.Limit, 0, len(c.Budgets))
	for _, b := range c.Budgets {
		out = append(out, budget.Limit{
			Name:        b.Name,
			Window:      budget.Window(strings.ToLower(b.Window)),
			LimitUSD:    b.LimitUSD,
			LimitTokens: b.LimitTokens,
			WarnAt:      b.WarnAt,
			CritAt:      b.CritAt,
			WorkflowID:  b.WorkflowID,
			AgentID:     b.AgentID,
			Basis:       strings.ToLower(b.Basis),
		})
	}
	return out
}

// WatchConfig tunes the active-mode background watcher.
type WatchConfig struct {
	// Interval between watcher evaluations. Default 15m, minimum 1m.
	Interval time.Duration `yaml:"interval"`
}

// EffectiveInterval returns the watcher cadence with defaults applied.
func (w WatchConfig) EffectiveInterval() time.Duration {
	if w.Interval <= 0 {
		return 15 * time.Minute
	}
	if w.Interval < time.Minute {
		return time.Minute
	}
	return w.Interval
}

// CoachingConfig tunes the waste detector behind `tokenops replay
// --workflow`, the tokenops_workflow_trace MCP tool, and the dashboard
// workflow view.
type CoachingConfig struct {
	// Delivery selects how far coaching goes. It is a ladder graded by
	// interference — how much of your session the coach is allowed to
	// take — and each rung adds a channel to the one below:
	//
	//   observe    — records everything, answers when asked. `tokenops
	//                coach prompts`, `coach replies`, `dx`, and the MCP
	//                coaching tools. The hooks stay installed but say
	//                nothing: they keep their ledgers, so `coach-hook
	//                stats` and `read-guard stats` still show what you
	//                are missing before you let them speak.
	//   advise     — observe, plus the coach speaks unprompted but never
	//                blocks: coach-hook nudges as session cost crosses a
	//                budget fraction. Advice you can ignore. The default.
	//   intervene  — advise, plus the coach acts: read-guard refuses a
	//                redundant re-read before it costs a token.
	//
	// The rungs are graded by interference rather than by who initiated,
	// because that is the question an operator actually has. "Does it
	// speak without being asked" puts a non-blocking nudge and a refused
	// tool call on the same rung, and those are not remotely the same
	// imposition.
	//
	// Note this is NOT Config.Mode. Mode decides whether TokenOps
	// intervenes in *traffic* — routing rules on the proxy, the spend
	// watcher. Delivery decides whether it intervenes in your *session*.
	// An operator can reasonably want either without the other.
	//
	// Empty means DeliveryAdvise, which is what the hooks did before this
	// key existed: coach-hook nudged, read-guard observed. Upgrading
	// changes nothing until you say so.
	Delivery string `yaml:"delivery,omitempty"`

	// Quiet rate-limits the coach's *proactive* channel — the nudges it
	// speaks without being asked. It has no effect on anything you ask
	// for: a direct question is never an interruption.
	Quiet QuietConfig `yaml:"quiet,omitempty"`

	// ContextLimits override the waste detector's context thresholds per
	// workflow-ID prefix. A matching entry replaces the built-in
	// profiles ("claude-code:", "codex:"); zero fields inherit the
	// detector defaults.
	ContextLimits []ContextLimitConfig `yaml:"context_limits"`
}

// QuietConfig bounds how often the coach may speak unprompted in one
// session. Only meaningful at delivery advise and intervene, where
// something speaks at all.
//
// Each individual finding already latches — a budget tier fires once per
// session and then stays quiet — so this is not about one finding
// repeating itself. It is about two *different* findings landing back to
// back and reading, to the operator, as nagging.
//
// Both knobs default to off, and off means the per-finding latches are
// the whole policy, which is what the hooks did before this key existed.
type QuietConfig struct {
	// MinInterval is the floor between two proactive nudges in one
	// session. A nudge suppressed by the floor is deferred, not dropped:
	// its finding stays unlatched and speaks at the next opportunity once
	// the floor has passed. Zero means no floor.
	MinInterval time.Duration `yaml:"min_interval,omitempty"`

	// MaxPerSession caps how many proactive nudges one session may carry.
	// Unlike the floor this drops rather than defers — a cap that queues
	// is not a cap. Zero means no cap, deferring entirely to the
	// per-finding latches.
	MaxPerSession int `yaml:"max_per_session,omitempty"`
}

// Validate rejects a quiet policy that cannot mean anything.
func (q QuietConfig) Validate() error {
	if q.MinInterval < 0 {
		return fmt.Errorf("coaching.quiet.min_interval must not be negative, got %s", q.MinInterval)
	}
	if q.MaxPerSession < 0 {
		return fmt.Errorf("coaching.quiet.max_per_session must not be negative, got %d", q.MaxPerSession)
	}
	return nil
}

// Delivery values for CoachingConfig.Delivery, in ascending order of
// interference.
const (
	DeliveryObserve   = "observe"
	DeliveryAdvise    = "advise"
	DeliveryIntervene = "intervene"
)

// ParseDelivery normalises a delivery level, falling back to reactive for
// anything unrecognised. Callers that need to reject a typo rather than
// absorb it use ValidateDelivery.
func ParseDelivery(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case DeliveryObserve:
		return DeliveryObserve
	case DeliveryIntervene:
		return DeliveryIntervene
	default:
		return DeliveryAdvise
	}
}

// ValidateDelivery reports whether s names a delivery level. Empty is
// valid and means the default.
func ValidateDelivery(s string) error {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", DeliveryObserve, DeliveryAdvise, DeliveryIntervene:
		return nil
	default:
		return fmt.Errorf("coaching.delivery %q: want one of %s, %s, %s",
			s, DeliveryObserve, DeliveryAdvise, DeliveryIntervene)
	}
}

// Delivery resolves the configured level, applying the default.
func (c CoachingConfig) DeliveryLevel() string { return ParseDelivery(c.Delivery) }

// AllowsPull reports whether coaching answers a direct question, through
// the CLI verbs or the MCP tools. True at every level: asking is never an
// interruption, and a tool that refuses to answer a direct question is a
// worse experience than one that stays quiet.
func (c CoachingConfig) AllowsPull() bool { return true }

// AllowsAdvice reports whether the coach may speak unprompted without
// blocking anything — the coach-hook nudge.
func (c CoachingConfig) AllowsAdvice() bool {
	switch c.DeliveryLevel() {
	case DeliveryAdvise, DeliveryIntervene:
		return true
	default:
		return false
	}
}

// AllowsIntervention reports whether the coach may interfere with the
// agent's work — read-guard refusing a redundant re-read.
func (c CoachingConfig) AllowsIntervention() bool {
	return c.DeliveryLevel() == DeliveryIntervene
}

// ContextLimitConfig is one per-prefix threshold override.
type ContextLimitConfig struct {
	WorkflowPrefix           string `yaml:"workflow_prefix"`
	MaxContextTokens         int64  `yaml:"max_context_tokens"`
	ContextGrowthLimitTokens int64  `yaml:"context_growth_limit_tokens"`
	MaxConsecutiveAgentLoops int    `yaml:"max_consecutive_agent_loops"`
	SystemRedundancyMin      int    `yaml:"system_redundancy_min"`
}

// WasteConfig maps coaching.context_limits into the waste detector's
// domain config. Shared by every adapter (CLI replay, MCP, dashboard)
// so all surfaces apply identical thresholds.
func (c CoachingConfig) WasteConfig() waste.Config {
	if len(c.ContextLimits) == 0 {
		return waste.Config{}
	}
	profiles := make([]waste.Profile, 0, len(c.ContextLimits))
	for _, l := range c.ContextLimits {
		profiles = append(profiles, waste.Profile{
			WorkflowPrefix:           l.WorkflowPrefix,
			MaxContextTokens:         l.MaxContextTokens,
			ContextGrowthLimitTokens: l.ContextGrowthLimitTokens,
			MaxConsecutiveAgentLoops: l.MaxConsecutiveAgentLoops,
			SystemRedundancyMin:      l.SystemRedundancyMin,
		})
	}
	return waste.Config{Profiles: profiles}
}

// OptimizerConfig tunes the optimizer pipeline shared by `tokenops
// replay` and the `tokenops_replay` MCP tool.
type OptimizerConfig struct {
	// Mode says what the optimizer may do with a request: "automatic"
	// rewrites it in flight, "in_request" refers the decision to you
	// through the MCP surface and forwards the request untouched, "off"
	// (the default) records what the rules would have done and changes
	// nothing.
	Mode OptimizerMode `yaml:"mode,omitempty"`
	// RoutingRules feed the model-routing optimizer: requests for
	// from_model are evaluated as if routed to to_model, and the replay
	// surfaces the projected $ savings. Empty leaves the router out of
	// the pipeline.
	RoutingRules []RoutingRuleConfig `yaml:"routing_rules"`
	// RoutingMinQuality is the quality floor below which a routing rule
	// is skipped silently. Zero falls back to the router default (0.7).
	RoutingMinQuality float64 `yaml:"routing_min_quality"`
	// SmartRouting decides routes nobody wrote a rule for, per turn and
	// from measured signal. Disabled by default.
	SmartRouting SmartRoutingConfig `yaml:"smart_routing,omitempty"`
	// CommandFmt configures deterministic command-output compression
	// (the `tokenops fmt` wrapper and the proxy tool-output optimizer).
	CommandFmt CommandFmtConfig `yaml:"command_fmt"`
}

// CommandFmtConfig tunes deterministic command-output compression. Loss is
// configured per command: Default applies to any command without an entry,
// Overrides maps a command token (e.g. "git", "docker") to its own level.
// Valid levels: "conservative", "balanced", "aggressive".
type CommandFmtConfig struct {
	// Default is the loss level for commands without an override. Empty
	// means "conservative" (noise-only, nothing semantic dropped).
	Default string `yaml:"default"`
	// Overrides maps a command token to its loss level.
	Overrides map[string]string `yaml:"overrides"`
	// EmitEvents, when true, makes `tokenops fmt` append an
	// OptimizationEvent (kind=command_fmt) to the local events store on
	// each compressed run so the dashboard and scorecard count the
	// savings. Best-effort: a store error never fails the wrapped command.
	EmitEvents bool `yaml:"emit_events"`
	// Formatters holds user-defined command formatters. They extend or
	// override the built-in catalog without recompiling: name a command,
	// list the regexes that mark critical lines (always preserved), and the
	// noise regexes to drop per loss level. User rules run through the same
	// critical-line survival guard as built-ins.
	Formatters []CommandFmtFormatter `yaml:"formatters"`
}

// CommandFmtFormatter is one user-defined formatter declaration.
type CommandFmtFormatter struct {
	// Command is the token this formatter handles (e.g. "mytool"). A
	// command matching a built-in overrides it.
	Command string `yaml:"command"`
	// Aliases are extra command tokens routed to this formatter.
	Aliases []string `yaml:"aliases"`
	// Critical lists regexes; a line matching any is preserved at every
	// loss level.
	Critical []string `yaml:"critical"`
	// Drop lists noise regexes removed at balanced and/or aggressive.
	Drop CommandFmtDrop `yaml:"drop"`
}

// CommandFmtDrop lists per-level noise regexes. Balanced rules apply at
// balanced AND aggressive; Aggressive rules apply only at aggressive.
type CommandFmtDrop struct {
	Balanced   []string `yaml:"balanced"`
	Aggressive []string `yaml:"aggressive"`
}

// RoutingRuleConfig is one "route X to Y" entry. FromModel supports a
// trailing "*" prefix match (e.g. "claude-fable-5*").
type RoutingRuleConfig struct {
	Provider  string `yaml:"provider"`
	FromModel string `yaml:"from_model"`
	ToModel   string `yaml:"to_model"`
	// Quality is the operator's confidence (0.0–1.0] that ToModel
	// preserves task quality for this traffic.
	Quality   float64  `yaml:"quality"`
	Fallbacks []string `yaml:"fallbacks"`
	// WhenClass scopes the rule to one kind of work: "mechanical" (a
	// terse directive over mostly tool traffic — safe to run on a
	// cheaper model) or "reasoning". Empty applies the rule to all
	// matching traffic, which is the historical behaviour.
	//
	// A scoped rule declines whenever the classifier cannot tell, so
	// turns that are ambiguous keep the model the client asked for.
	WhenClass string `yaml:"when_class,omitempty"`
	// WhenWindowPctAbove scopes the rule to periods when the plan's
	// rate-limit window is at least this full (0–100). Zero applies it
	// regardless.
	//
	// On a flat-rate plan this is the objective that matters: requests
	// bill $0.00 at the margin, so the resource that runs out is the
	// window. Gating on it keeps you on your best model while there is
	// headroom and conserves it only when there is not. A rule scoped
	// this way stays idle whenever the window cannot be measured.
	WhenWindowPctAbove float64 `yaml:"when_window_pct_above,omitempty"`
}

// Validate checks one rule on its own, so the surfaces that write a rule
// (`tokenops routing rule set`, tokenops_routing_rule_set) can refuse it
// before touching the file and name the argument that was wrong. Checked
// only at whole-config write time, the refusal pointed at
// "optimizer.routing_rules[3]" — an index into a file the caller never
// saw — and a whitespace to_model passed as present.
func (r RoutingRuleConfig) Validate() error {
	for _, f := range []struct{ name, v string }{
		{"provider", r.Provider}, {"from_model", r.FromModel}, {"to_model", r.ToModel},
	} {
		if strings.TrimSpace(f.v) == "" {
			return fmt.Errorf("%s is required", f.name)
		}
	}
	switch strings.ToLower(r.WhenClass) {
	case "", string(taskclass.Mechanical), string(taskclass.Reasoning):
	default:
		return fmt.Errorf("when_class must be %q or %q, got %q",
			taskclass.Mechanical, taskclass.Reasoning, r.WhenClass)
	}
	if r.WhenWindowPctAbove < 0 || r.WhenWindowPctAbove > 100 {
		return fmt.Errorf("when_window_pct_above must be in [0,100], got %g", r.WhenWindowPctAbove)
	}
	if r.Quality <= 0 || r.Quality > 1 {
		return fmt.Errorf("quality must be in (0,1], got %g", r.Quality)
	}
	return nil
}

// SmartRoutingConfig is the rules-free routing policy: instead of a
// from/to pair written once, decide each turn from what kind of work it
// is, how full the plan's rate-limit window is, and what the pricing
// table currently calls cheapest.
//
// It only ever routes downwards, only for work the classifier is
// confident is mechanical, and only while the window is genuinely tight.
// The preferred-model ceiling still outranks it, so it cannot raise a
// bill.
type SmartRoutingConfig struct {
	// Enabled turns the policy on. Off is the behaviour that shipped
	// before it existed: a request no rule matches is left alone.
	Enabled bool `yaml:"enabled,omitempty"`
	// WindowPctAbove is how full the window must be before conserving
	// starts. Zero takes the router default (70).
	//
	// There is deliberately no "always" setting: on a flat-rate plan a
	// request costs nothing at the margin, so routing down while there is
	// headroom trades quality for a saving that does not exist.
	WindowPctAbove float64 `yaml:"window_pct_above,omitempty"`
	// Quality is the confidence that a mechanical turn survives the
	// cheapest model, gated by routing_min_quality like any rule. Zero
	// takes the router default (0.75).
	Quality float64 `yaml:"quality,omitempty"`
	// Intervention is how hard the per-turn guard pushes: advise states
	// the case, delegate additionally marks work that may be handed to a
	// subagent on the cheaper model, auto allows that for every kind it
	// is confident about, off disables it. Empty means advise.
	Intervention string `yaml:"intervention,omitempty"`
	// AutoKinds are the task kinds delegation applies to under
	// "delegate" — typically the cheap, low-judgement ones such as
	// lookup and research. Ignored under "auto".
	AutoKinds []string `yaml:"auto_kinds,omitempty"`
	// Models lists the models actually on offer, per provider.
	//
	// Required for the guard to say anything. The rate card keeps every
	// model a vendor ever priced, including retired ones that sit below
	// the current cheap model and above the current flagship; ranking
	// across all of them recommends a model nobody can still choose.
	// Naming the live set is what makes the tiers mean today's menu.
	Models map[string][]string `yaml:"models,omitempty"`
}

// Validate rejects a policy that cannot mean anything.
func (s SmartRoutingConfig) Validate() error {
	if s.WindowPctAbove < 0 || s.WindowPctAbove > 100 {
		return fmt.Errorf("optimizer.smart_routing.window_pct_above must be in [0,100], got %g", s.WindowPctAbove)
	}
	if s.Quality < 0 || s.Quality > 1 {
		return fmt.Errorf("optimizer.smart_routing.quality must be in [0,1], got %g", s.Quality)
	}
	return nil
}

// RouterConfig maps optimizer.routing_rules into the router's domain
// config. Returns nil when no rules are configured. Shared by the CLI
// replay, MCP serve, and the daemon's active-mode proxy so all
// surfaces route identically.
func (o OptimizerConfig) RouterConfig() *router.Config {
	if len(o.RoutingRules) == 0 && !o.SmartRouting.Enabled {
		return nil
	}
	rules := make([]router.Rule, 0, len(o.RoutingRules))
	for _, r := range o.RoutingRules {
		rules = append(rules, router.Rule{
			Provider:           eventschema.Provider(r.Provider),
			FromModel:          r.FromModel,
			ToModel:            r.ToModel,
			Quality:            r.Quality,
			Fallbacks:          r.Fallbacks,
			WhenClass:          strings.ToLower(r.WhenClass),
			WhenWindowPctAbove: r.WhenWindowPctAbove,
		})
	}
	return &router.Config{
		Rules:       rules,
		MinQuality:  o.RoutingMinQuality,
		ProposeOnly: o.Mode.Proposes(),
		ObserveOnly: o.Mode.ObserveOnly(),
		Policy: router.Policy{
			Enabled:        o.SmartRouting.Enabled,
			WindowPctAbove: o.SmartRouting.WindowPctAbove,
			Quality:        o.SmartRouting.Quality,
		},
	}
}

// PricingConfig points at an optional YAML rate file that is layered on
// top of the built-in list-price catalog (spend.TableWithOverrides).
// Use it to price newly released models before a tokenops upgrade, or
// to apply negotiated rates. Same schema as the embedded pricing.yaml.
type PricingConfig struct {
	Path string `yaml:"path"`
	// Refresh keeps the rate card current without anyone remembering to
	// run `tokenops pricing refresh`.
	Refresh PricingRefreshConfig `yaml:"refresh,omitempty"`
}

// PricingRefreshConfig governs the daemon's automatic rate-card refresh.
//
// This is the one outbound call tokenops makes on its own. It fetches a
// public rate card (LiteLLM's model_prices_and_context_window.json) and
// sends nothing: no prompt, no file, no identifier, no usage. The privacy
// claim is about content, and no content is involved — but a tool that
// starts talking to the network without saying so has spent trust it
// cannot buy back, which is why this is documented and switchable rather
// than quietly always-on.
type PricingRefreshConfig struct {
	// Disabled turns the automatic refresh off. Off by absence would be
	// the safer default for a network call, but a rate card nobody
	// refreshes is how a session gets priced at $0 on a model released
	// after the binary — which is the failure this exists to prevent.
	Disabled bool `yaml:"disabled,omitempty"`
	// Interval is how often to check. Zero takes DefaultPricingRefresh.
	// Values below the floor are raised to it: rate cards change on the
	// order of weeks, and hammering a public file helps nobody.
	Interval time.Duration `yaml:"interval,omitempty"`
}

// Pricing-refresh defaults. A day is far more often than list prices
// move, and still catches a model released overnight before the next
// session is mispriced.
const (
	DefaultPricingRefresh = 24 * time.Hour
	MinPricingRefresh     = time.Hour
)

// Enabled reports whether the daemon should refresh the rate card.
func (p PricingRefreshConfig) Enabled() bool { return !p.Disabled }

// Every resolves the check interval, applying the default and the floor.
func (p PricingRefreshConfig) Every() time.Duration {
	if p.Interval <= 0 {
		return DefaultPricingRefresh
	}
	if p.Interval < MinPricingRefresh {
		return MinPricingRefresh
	}
	return p.Interval
}

// DashboardConfig gates /dashboard + /api/* behind a shared-secret
// token. AdminToken empty → daemon mints + persists one to
// ~/.tokenops/dashboard.token on first start. Setting it explicitly
// (env-substituted via the loader) lets ops roll the secret without
// touching disk state.
type DashboardConfig struct {
	AdminToken string `yaml:"admin_token"`
}

// PlanLimit is the operator-supplied side of a plan whose limit this tool
// cannot know: the org spend limit an admin set in the vendor console, and
// the rate a negotiated contract actually bills at.
type PlanLimit struct {
	// SpendLimitUSD is the org's configured cap for the window below.
	// Required for a spend-denominated plan; `plan set` refuses the
	// binding without it rather than defaulting to a number nobody chose.
	SpendLimitUSD float64 `yaml:"spend_limit_usd,omitempty"`
	// Window is the period the limit applies over: monthly (default),
	// weekly or daily, matching how the console states it.
	Window string `yaml:"window,omitempty"`
	// RateFactor scales measured spend to a negotiated rate.
	//
	// Enterprise contracts are frequently discounted off list while this
	// tool costs from the public rate card, so a console limit would
	// otherwise be compared against an overstatement — and the error is
	// invisible, which is the kind worth refusing to ship. Zero or one
	// means list price.
	RateFactor float64 `yaml:"rate_factor,omitempty"`
}

// MDNSConfig gates the Bonjour/mDNS advertisement that makes the dashboard
// reachable at http://tokenops.local:<port>.
//
// It had no config at all: the daemon advertised unconditionally, on every
// interface, under an instance name built from the machine's hostname. A
// local-first tool broadcasting the operator's computer name to every
// network they join should at least be something they can turn off.
type MDNSConfig struct {
	// Enabled forces advertising on or off. Nil — the default — advertises
	// only when the daemon binds an address the LAN can actually reach,
	// because a record pointing at 127.0.0.1 tells a peer nothing while
	// still broadcasting the name.
	Enabled *bool `yaml:"enabled,omitempty"`
	// InstanceName replaces the hostname in the advertised service name,
	// for operators who want tokenops.local without publishing what their
	// laptop is called. Empty uses the hostname, as before.
	InstanceName string `yaml:"instance_name,omitempty"`
}

// VendorUsageConfig wires the vendor-side usage pollers. Each provider
// has its own block because authentication, polling cadence, and the
// signal quality story differ. ClaudeCode reads the local Claude Code
// stats cache; future blocks (anthropic admin API, openai usage)
// add here as separate sub-structs.
type VendorUsageConfig struct {
	ClaudeCode       ClaudeCodeUsageConfig      `yaml:"claude_code"`
	ClaudeCodeJSONL  ClaudeCodeJSONLUsageConfig `yaml:"claude_code_jsonl"`
	CodexJSONL       CodexJSONLUsageConfig      `yaml:"codex_jsonl"`
	OpenCode         OpenCodeUsageConfig        `yaml:"opencode"`
	Anthropic        AnthropicUsageConfig       `yaml:"anthropic"`
	GitHubCopilot    GitHubCopilotUsageConfig   `yaml:"github_copilot"`
	Cursor           CursorUsageConfig          `yaml:"cursor"`
	ClaudeUsageMeter ClaudeUsageMeterConfig     `yaml:"claude_usage_meter"`
}

// GitHubCopilotUsageConfig wires the api.github.com/copilot_internal/user
// poller. OAuthToken empty → poller reads it from
// ~/.config/github-copilot/apps.json (or hosts.json) — same file the
// IDE plugins use. Interval defaults to 2 minutes.
type GitHubCopilotUsageConfig struct {
	Enabled    bool          `yaml:"enabled"`
	OAuthToken string        `yaml:"oauth_token"`
	Interval   time.Duration `yaml:"interval"`
}

// CursorUsageConfig wires the cursor.com/api/usage poller. Cookie +
// UserID must be set (extract from the Cursor IDE devtools, or via a
// future state.vscdb auto-discovery). Empty means the poller stays
// idle. Interval defaults to 2 minutes.
type CursorUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Cookie   string        `yaml:"cookie"`
	UserID   string        `yaml:"user_id"`
	Interval time.Duration `yaml:"interval"`
}

// ClaudeUsageMeterConfig wires the claude.ai usage-meter
// poller. SessionKey extracted from the operator's browser (devtools
// → Application → Cookies → sessionKey). OrgID empty → poller
// resolves it via /api/organizations on first scan. Interval defaults
// to 5 minutes; Anthropic's cookie tier rate-limits aggressive
// polling and the data shifts on a 5-hour bucket cadence anyway.
type ClaudeUsageMeterConfig struct {
	Enabled    bool          `yaml:"enabled"`
	SessionKey string        `yaml:"session_key"`
	OrgID      string        `yaml:"org_id"`
	Interval   time.Duration `yaml:"interval"`
	// FromBrowser re-reads the session and Cloudflare clearance cookies
	// from the local browser as the daemon polls. claude.ai's bot check
	// refuses a request without a clearance cookie, and that cookie
	// expires within hours: a meter that stored one at setup works for an
	// afternoon and is refused thereafter. macOS asks to allow the
	// keychain read once per installed version.
	FromBrowser bool `yaml:"from_browser,omitempty"`
	// Browser limits that read to one browser by name; empty searches.
	Browser string `yaml:"browser,omitempty"`
}

// CodexJSONLUsageConfig enables the Codex CLI session-log reader.
// Parses ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl. Empty
// Root defaults to ~/.codex/sessions.
type CodexJSONLUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// OpenCodeUsageConfig enables the opencode session reader, which opens
// opencode's SQLite store read-only and surfaces per-assistant-turn token
// usage. Empty Root defaults to ~/.local/share/opencode/opencode.db
// (honouring XDG_DATA_HOME).
type OpenCodeUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// ClaudeCodeJSONLUsageConfig enables the per-turn JSONL reader that
// parses ~/.claude/projects/**/*.jsonl. This is the high-confidence
// successor to the v0.10.2 stats-cache reader (which lags by days on
// active users). Empty Root defaults to ~/.claude/projects.
type ClaudeCodeJSONLUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// AnthropicUsageConfig wires the Anthropic Admin API poller. AdminKey
// must be a sk-ant-admin-* key minted in the Claude Console; without
// one the poller stays idle but the daemon still starts. Interval
// defaults to 5 minutes (the API freshness lag).
type AnthropicUsageConfig struct {
	Enabled     bool          `yaml:"enabled"`
	AdminKey    string        `yaml:"admin_key"`
	Interval    time.Duration `yaml:"interval"`
	BucketWidth string        `yaml:"bucket_width"`
}

// ClaudeCodeUsageConfig enables reading ~/.claude/stats-cache.json and
// emitting envelopes for the per-model daily token totals Claude Code
// records there. Empty Path defaults to the conventional location.
// Interval below 15s is clamped at the poller level to avoid
// hammering the file on caches that rotate frequently.
type ClaudeCodeUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Path     string        `yaml:"path"`
	Interval time.Duration `yaml:"interval"`
}

// ResilienceConfig wraps each provider proxy route with
// fortify's CircuitBreakerStream. Off by default; opt in to gain
// per-stream FirstByte / Idle / Total deadlines and per-provider
// circuit breakers across SSE streams. Zero-valued deadlines disable
// the corresponding watchdog (at least one must be positive when
// enabled).
type ResilienceConfig struct {
	Enabled          bool          `yaml:"enabled"`
	FirstByteTimeout time.Duration `yaml:"first_byte_timeout"`
	IdleTimeout      time.Duration `yaml:"idle_timeout"`
	TotalTimeout     time.Duration `yaml:"total_timeout"`
	// FailureThreshold is the consecutive-failure count that trips
	// the breaker for a given route. Defaults to 5 when zero.
	FailureThreshold uint32 `yaml:"failure_threshold"`
}

// RulesConfig wires the Rule Intelligence subsystem (issue #12).
// Enabled gates the /api/rules/* dashboard endpoints. Root is the
// repository the daemon scans on each request — defaults to the daemon's
// working directory when unset. RepoID is an opaque identifier prepended
// to rule SourceIDs (allows cross-repo aggregation without leaking repo
// names through telemetry).
type RulesConfig struct {
	Enabled bool   `yaml:"enabled"`
	Root    string `yaml:"root"`
	RepoID  string `yaml:"repo_id"`
}

// OTelConfig configures the optional OTLP/HTTP/JSON telemetry exporter.
// When Enabled, every envelope emitted to the bus is also forwarded to
// Endpoint. Headers (e.g. tenant tokens) are sent on every request.
type OTelConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Endpoint       string            `yaml:"endpoint"`
	Headers        map[string]string `yaml:"headers"`
	ServiceName    string            `yaml:"service_name"`
	ServiceVersion string            `yaml:"service_version"`
	// Redact, when true, runs the redaction pipeline before exporting.
	// Default true; explicit false disables redaction (use with care).
	Redact *bool `yaml:"redact"`
}

// RedactEnabled reports whether redaction should be applied to OTLP
// exports. Defaults to true when unset.
func (o OTelConfig) RedactEnabled() bool {
	if o.Redact == nil {
		return true
	}
	return *o.Redact
}

// StorageConfig configures the local event store. When Enabled, the
// daemon opens a sqlite database at Path and emits PromptEvents into it
// via an async bus.
type StorageConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// RetentionConfig is an opt-in event-store prune. Empty Keep disables
// the scheduler entirely so a fresh install never deletes events. The
// daemon starts the worker only when at least one keep window is
// positive. Audit log rows are never pruned.
type RetentionConfig struct {
	// Interval is how often the pruner wakes. Zero defaults to 1h when
	// the scheduler is running.
	Interval time.Duration `yaml:"interval,omitempty"`
	// Keep maps an event type (prompt, workflow, optimization,
	// coaching, rule_source, rule_analysis) to a window. Values accept
	// Go durations plus a "d" day suffix (30d = 720h).
	Keep map[string]string `yaml:"keep,omitempty"`
	// KeepBySource maps a source ("opencode", "codex-jsonl",
	// "claude-code-jsonl", ...) to its own window, overriding the
	// type window for that source's rows.
	//
	// Every vendor-usage reader writes type "prompt", so keep alone
	// cannot tell one client from another: a window short enough for the
	// live stream also deletes the imported history of a client last
	// used months ago, which is exactly what a backfill consists of.
	// A source entry of "forever" (or "never", or "0") keeps that
	// source's rows indefinitely.
	//
	// Keys may be qualified with an event type ("prompt:opencode") when a
	// source emits more than one; a bare source name applies to prompt
	// events, which is what every usage reader writes.
	KeepBySource map[string]string `yaml:"keep_by_source,omitempty"`
	// Reclaim runs a VACUUM after a prune that deleted rows so the freed
	// pages return to the filesystem. Without it SQLite keeps them on
	// its freelist and the database file never shrinks — pruning frees
	// space the operator cannot see. Costs a full rewrite under a write
	// lock, so it is opt-in.
	Reclaim bool `yaml:"reclaim,omitempty"`
}

// Enabled reports whether any keep window is set so the daemon should
// start the pruner.
func (c RetentionConfig) Enabled() bool {
	for _, raw := range c.Keep {
		d, err := ParseKeepDuration(raw)
		if err == nil && d > 0 {
			return true
		}
	}
	// A source window alone is enough to want the pruner running: it may
	// be the only rule, and it may be shorter than any type window.
	for _, raw := range c.KeepBySource {
		d, err := ParseKeepDuration(raw)
		if err == nil && d > 0 {
			return true
		}
	}
	return false
}

// knownRetentionTypes are the event types the pruner accepts. audit_log
// is intentionally absent — operators rely on it for forensic queries.
var knownRetentionTypes = map[string]struct{}{
	"prompt":        {},
	"workflow":      {},
	"optimization":  {},
	"coaching":      {},
	"rule_source":   {},
	"rule_analysis": {},
}

// ParseKeepDuration accepts Go durations plus a day suffix ("30d").
func ParseKeepDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "0s" {
		return 0, nil
	}
	// A zero window means "never prune this". Spelling it out keeps a
	// config from being read as "keep for zero time", which in a delete
	// path is the opposite of what it does.
	switch strings.ToLower(s) {
	case "forever", "never":
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q", s)
		}
		if n < 0 {
			return 0, fmt.Errorf("duration must be non-negative, got %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration must be non-negative, got %s", s)
	}
	return d, nil
}

// TLSConfig configures TLS termination on the local proxy.
type TLSConfig struct {
	// Enabled toggles HTTPS. When false, the proxy serves plain HTTP.
	Enabled bool `yaml:"enabled"`
	// CertDir is the directory the auto-minted CA + leaf bundle lives in.
	// On first run TokenOps creates the bundle here; on subsequent runs
	// the same files are reused. Default ~/.tokenops/certs.
	CertDir string `yaml:"cert_dir"`
	// Hostnames are extra DNS SANs added to the leaf cert. Loopback names
	// (localhost, 127.0.0.1, ::1) are always included.
	Hostnames []string `yaml:"hostnames"`
}

// LogConfig configures the structured logger.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// ShutdownConfig configures graceful shutdown behaviour.
type ShutdownConfig struct {
	Timeout time.Duration `yaml:"timeout"`
}

// Default returns the built-in defaults. The daemon is local-first by default
// and binds to loopback so a fresh install never accidentally exposes the
// proxy on the network.
func Default() Config {
	return Config{
		Listen: "127.0.0.1:7878",
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Shutdown: ShutdownConfig{
			Timeout: 15 * time.Second,
		},
	}
}

// Load resolves configuration in order of precedence: defaults, optional YAML
// file (path may be empty), and environment variables. Environment variables
// always win.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %q: %w", path, err)
		}
		if err := checkRenamedKeys(data, path); err != nil {
			return Config{}, err
		}
	}

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the configuration for unrecoverable errors.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address must not be empty")
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q", c.Log.Level)
	}
	if err := ValidateDelivery(c.Coaching.Delivery); err != nil {
		return err
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("invalid log format %q", c.Log.Format)
	}
	if c.Shutdown.Timeout <= 0 {
		return fmt.Errorf("shutdown.timeout must be positive, got %s", c.Shutdown.Timeout)
	}
	if c.OTel.Enabled && c.OTel.Endpoint == "" {
		return errors.New("otel.endpoint must be set when otel.enabled is true")
	}
	if c.Resilience.Enabled {
		if c.Resilience.FirstByteTimeout <= 0 && c.Resilience.IdleTimeout <= 0 && c.Resilience.TotalTimeout <= 0 {
			return errors.New("resilience.enabled requires at least one positive timeout (first_byte_timeout, idle_timeout, total_timeout)")
		}
	}
	for provider, planName := range c.Plans {
		if err := plans.Validate(planName); err != nil {
			return fmt.Errorf("plans[%s]: %w", provider, err)
		}
	}
	if q := c.Optimizer.RoutingMinQuality; q < 0 || q > 1 {
		return fmt.Errorf("optimizer.routing_min_quality must be in [0,1], got %g", q)
	}
	for i, r := range c.Optimizer.RoutingRules {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("optimizer.routing_rules[%d]: %w", i, err)
		}
	}
	if err := c.Coaching.Quiet.Validate(); err != nil {
		return err
	}
	if err := c.Optimizer.SmartRouting.Validate(); err != nil {
		return err
	}
	for i, l := range c.Coaching.ContextLimits {
		if l.WorkflowPrefix == "" {
			return fmt.Errorf("coaching.context_limits[%d]: workflow_prefix is required", i)
		}
		if l.MaxContextTokens < 0 || l.ContextGrowthLimitTokens < 0 ||
			l.MaxConsecutiveAgentLoops < 0 || l.SystemRedundancyMin < 0 {
			return fmt.Errorf("coaching.context_limits[%d]: thresholds must be non-negative", i)
		}
	}
	switch strings.ToLower(c.Mode) {
	case "", ModePassive, ModeActive:
	default:
		return fmt.Errorf("mode must be %q or %q, got %q", ModePassive, ModeActive, c.Mode)
	}
	for i, b := range c.Budgets {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("budgets[%d]: %w", i, err)
		}
	}
	if !c.Optimizer.Mode.Valid() {
		return fmt.Errorf("optimizer.mode must be %q, %q, or %q, got %q",
			OptimizerAutomatic, OptimizerInRequest, OptimizerOff, c.Optimizer.Mode)
	}
	for provider, model := range c.PreferredModels {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("preferred_models[%s]: model must not be empty", provider)
		}
	}
	if c.Watch.Interval < 0 {
		return fmt.Errorf("watch.interval must be non-negative, got %s", c.Watch.Interval)
	}
	if c.Retention.Interval < 0 {
		return fmt.Errorf("retention.interval must be non-negative, got %s", c.Retention.Interval)
	}
	for name, raw := range c.Retention.Keep {
		if _, ok := knownRetentionTypes[name]; !ok {
			return fmt.Errorf("retention.keep: unknown event type %q", name)
		}
		if _, err := ParseKeepDuration(raw); err != nil {
			return fmt.Errorf("retention.keep[%s]: %w", name, err)
		}
	}
	for key, raw := range c.Retention.KeepBySource {
		typ, src := SplitRetentionSourceKey(key)
		if src == "" {
			return fmt.Errorf("retention.keep_by_source: empty source in key %q", key)
		}
		if _, ok := knownRetentionTypes[typ]; !ok {
			return fmt.Errorf("retention.keep_by_source[%s]: unknown event type %q", key, typ)
		}
		if _, err := ParseKeepDuration(raw); err != nil {
			return fmt.Errorf("retention.keep_by_source[%s]: %w", key, err)
		}
	}
	return nil
}

// SplitRetentionSourceKey splits a keep_by_source key into its event type
// and source. A bare source name means prompt events, which is what every
// vendor-usage reader writes.
func SplitRetentionSourceKey(key string) (eventType, source string) {
	if typ, src, ok := strings.Cut(key, ":"); ok {
		return strings.TrimSpace(typ), strings.TrimSpace(src)
	}
	return "prompt", strings.TrimSpace(key)
}

// Blockers returns the stable, machine-readable list of subsystem gates
// that prevent the daemon from returning populated data. Order is fixed
// so callers can diff successive snapshots. Returns a non-nil empty
// slice when nothing is gated so JSON serialises as [] not null.
func (c Config) Blockers() []string {
	blockers := []string{}
	if !c.Storage.Enabled {
		blockers = append(blockers, "storage_disabled")
	}
	if !c.Rules.Enabled {
		blockers = append(blockers, "rules_disabled")
	}
	if len(c.Providers) == 0 {
		blockers = append(blockers, "providers_unconfigured")
	}
	// A rules.root that no longer exists fails silently: the analyser
	// walks an empty tree and reports success, so a moved or renamed
	// repository degrades rule intelligence to nothing with no signal.
	// An empty root is fine — it means "use the working directory".
	if c.Rules.Enabled && c.Rules.Root != "" && !dirExists(c.Rules.Root) {
		blockers = append(blockers, "rules_root_missing")
	}
	return blockers
}

// dirExists reports whether path is a directory we can stat.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// NextActionsFor maps blockers to operator-facing remediation steps and
// deduplicates so a single `tokenops init` call surfaces once even when
// it resolves multiple blockers. Returns a non-nil empty slice when
// blockers is empty.
func NextActionsFor(blockers []string) []string {
	out := []string{}
	if len(blockers) == 0 {
		return out
	}
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, b := range blockers {
		switch b {
		case "storage_disabled", "rules_disabled":
			add("run `tokenops init` then restart the daemon")
		case "providers_unconfigured":
			add("run `tokenops provider set <name> <url>` (e.g. `tokenops provider set anthropic https://api.anthropic.com`)")
		case "rules_root_missing":
			add("set rules.root in config.yaml to a directory that exists (the configured path is gone — did the repo move?)")
		}
	}
	return out
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("TOKENOPS_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("TOKENOPS_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("TOKENOPS_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("TOKENOPS_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Shutdown.Timeout = d
		} else if secs, err := strconv.Atoi(v); err == nil {
			cfg.Shutdown.Timeout = time.Duration(secs) * time.Second
		}
	}
	if v := os.Getenv("TOKENOPS_TLS_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.TLS.Enabled = true
		case "0", "false", "no", "off":
			cfg.TLS.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_TLS_CERT_DIR"); v != "" {
		cfg.TLS.CertDir = v
	}
	if v := os.Getenv("TOKENOPS_STORAGE_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.Storage.Enabled = true
		case "0", "false", "no", "off":
			cfg.Storage.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_STORAGE_PATH"); v != "" {
		cfg.Storage.Path = v
	}
	if v := os.Getenv("TOKENOPS_OTEL_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.OTel.Enabled = true
		case "0", "false", "no", "off":
			cfg.OTel.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_OTEL_ENDPOINT"); v != "" {
		cfg.OTel.Endpoint = v
	}
	if v := os.Getenv("TOKENOPS_OTEL_SERVICE_NAME"); v != "" {
		cfg.OTel.ServiceName = v
	}
	if v := os.Getenv("TOKENOPS_PRICING_PATH"); v != "" {
		cfg.Pricing.Path = v
	}
	for _, key := range []string{"openai", "anthropic", "gemini"} {
		envKey := "TOKENOPS_PROVIDER_" + strings.ToUpper(key) + "_URL"
		if v := os.Getenv(envKey); v != "" {
			if cfg.Providers == nil {
				cfg.Providers = make(map[string]string, 3)
			}
			cfg.Providers[key] = v
		}
		planEnvKey := "TOKENOPS_PLAN_" + strings.ToUpper(key)
		if v := os.Getenv(planEnvKey); v != "" {
			if cfg.Plans == nil {
				cfg.Plans = make(map[string]string, 3)
			}
			cfg.Plans[key] = v
		}
	}
}

// renamedKeys maps a key that no longer exists to what replaced it.
//
// A renamed YAML key is silently ignored by the decoder, so a config that
// still uses the old spelling loads cleanly and the feature simply never
// runs — the exact shape of failure this project keeps removing. These are
// refused loudly instead, naming the replacement, because the error message
// is the only migration aid a clean break offers.
var renamedKeys = map[string]string{
	"anthropic_cookie": "claude_usage_meter",
}

// checkRenamedKeys refuses a config still using a retired key.
func checkRenamedKeys(data []byte, path string) error {
	var raw struct {
		VendorUsage map[string]any `yaml:"vendor_usage"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil // the real decode already reported anything fatal
	}
	for old, replacement := range renamedKeys {
		if _, present := raw.VendorUsage[old]; !present {
			continue
		}
		return fmt.Errorf(
			"config %q: vendor_usage.%s was renamed to vendor_usage.%s.\n"+
				"  The old name described how the data was fetched rather than what it gives you.\n"+
				"  Rename the key (its contents are unchanged), or re-run "+
				"`tokenops vendor-usage setup %s`.\n"+
				"  Events already stored under the old source tag keep it and are not counted "+
				"against the new name",
			path, old, replacement, strings.ReplaceAll(replacement, "_", "-"))
	}
	return nil
}
