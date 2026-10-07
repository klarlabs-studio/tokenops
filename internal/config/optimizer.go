package config

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// OptimizerConfig tunes the optimizer pipeline shared by `tokenops
// replay` and the “tokenops replay“ MCP tool.
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
// (`tokenops routing rule set`, tokenops_routing (action=set_rule)) can refuse it
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
	// The route guard and the control policy each parse this string with
	// their own fallback, so a value neither names would mean advise to
	// one and observe-only to the other.
	switch strings.ToLower(strings.TrimSpace(s.Intervention)) {
	case "", "off", "false", "no", "advise", "delegate", "auto":
	default:
		return fmt.Errorf("optimizer.smart_routing.intervention must be off, advise, delegate, or auto, got %q", s.Intervention)
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
	return o.routerConfig()
}

func (o OptimizerConfig) routerConfig() *router.Config {
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
			Models:         o.SmartRouting.offered(),
		},
	}
}

// offered keys the models on offer by provider for the router.
func (s SmartRoutingConfig) offered() map[eventschema.Provider][]string {
	if len(s.Models) == 0 {
		return nil
	}
	out := make(map[eventschema.Provider][]string, len(s.Models))
	for p, models := range s.Models {
		out[eventschema.Provider(p)] = models
	}
	return out
}

// RouterConfig is the optimizer's router configuration with the model
// policy applied, so no route lands on a model the operator ruled out and
// a request for one is moved off it. Nil when there is neither routing
// nor a policy to apply.
func (c Config) RouterConfig() *router.Config {
	policy := c.ModelPolicy.Policy()
	if policy.Empty() {
		return c.Optimizer.RouterConfig()
	}
	rc := c.Optimizer.routerConfig()
	rc.Permits = policy.Permits
	return rc
}
