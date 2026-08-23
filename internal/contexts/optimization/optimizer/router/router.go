// Package router hosts the adaptive model-routing optimizer. It rewrites
// the request's model field according to a configurable policy: a small
// table of "if request asks for X, route to Y instead" rules, with a
// fallback chain when the preferred target is unavailable. Routing
// decisions are returned as Recommendations carrying QualityScore + the
// projected token / spend savings so the pipeline can record (passive)
// or apply (interactive) the change.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Rule is a single routing entry: requests asking for FromModel are
// routed to ToModel (per-provider). Quality is the operator's
// confidence that ToModel preserves task quality (0.0–1.0). The pipeline
// + qualitygate can use this score to gate the route.
type Rule struct {
	Provider eventschema.Provider
	// FromModel matches when the inbound model equals this string or
	// (when it ends with "*") begins with the prefix.
	FromModel string
	ToModel   string
	Quality   float64
	// Fallbacks is consulted in order if ToModel is unavailable. The
	// router itself does not probe availability; callers can wire a
	// healthcheck via Config.IsAvailable.
	Fallbacks []string
	// WhenWindowPctAbove scopes the rule to periods when the plan's
	// rate-limit window is at least this full (0–100). Zero applies the
	// rule regardless of window state.
	//
	// This is the objective that matters on a flat-rate subscription:
	// requests cost $0.00 at the margin, so the scarce resource is the
	// window, not money. Gating on pressure keeps the operator on their
	// best model while they can afford it and preserves headroom only
	// when it is actually running out.
	//
	// A rule scoped this way stays idle whenever the window cannot be
	// measured. Degrading quality on the strength of a meter that is not
	// reporting would be acting on a shortage that may not exist.
	WhenWindowPctAbove float64
	// WhenClass scopes the rule to one kind of work, as classified by
	// taskclass ("mechanical" or "reasoning"). Empty applies the rule
	// unconditionally, which is the historical behaviour.
	//
	// A scoped rule declines whenever the classifier abstains. Routing a
	// reasoning turn down to a cheaper model is a quality trade the
	// operator did not ask for, so an unclassifiable turn is left alone
	// rather than guessed at.
	WhenClass string
}

// Config tunes the router.
type Config struct {
	// Rules is the routing table. The first match wins; rules with a
	// "*" suffix match by prefix.
	Rules []Rule
	// IsAvailable, when set, gates each candidate target. Returns false
	// to skip ToModel and try the next fallback. Default: always true.
	IsAvailable func(provider eventschema.Provider, model string) bool
	// MinQuality is the floor for emitting a recommendation. Routes
	// with Quality below this are skipped silently. Default 0.7.
	MinQuality float64
	// Classify tunes the task classifier used by class-scoped rules.
	// Zero values take the taskclass defaults.
	Classify taskclass.Config
	// PreferredModel returns the operator's preferred model for a
	// provider, which acts as a ceiling: a rule routing to something
	// pricier is refused and referred to them instead of applied. Nil,
	// or an empty return, disables the ceiling and every rule applies
	// unconditionally — the behaviour before this existed.
	PreferredModel func(eventschema.Provider) string
	// UpgradeDecision reports the operator's standing answer for a route
	// that exceeds the ceiling. Nil is equivalent to always Pending.
	UpgradeDecision func(provider eventschema.Provider, from, to string) Decision
	// OnProposal is invoked when an upgrade is refused for want of an
	// answer, exactly once per request. Wire it to whatever surfaces the
	// choice to the operator. It must not block: this runs in the
	// request path.
	OnProposal func(Proposal)
	// WindowPressure reports how full the provider's rate-limit window
	// is, as a percentage, and whether that reading is trustworthy at
	// all. Rules carrying WhenWindowPctAbove consult it. Nil means no
	// window signal, and every such rule stays idle.
	//
	// It runs in the request path, so it must be cheap and non-blocking:
	// wire it to a cached reading refreshed in the background, never to
	// a live query.
	WindowPressure func(eventschema.Provider) (pct float64, ok bool)
}

// Router is the Optimizer implementation.
type Router struct {
	cfg   Config
	spend *spend.Engine
}

// New constructs a Router. spendEng may be nil — savings then surface as
// raw token deltas without monetary values.
func New(cfg Config, spendEng *spend.Engine) *Router {
	if cfg.MinQuality <= 0 {
		cfg.MinQuality = 0.7
	}
	if cfg.IsAvailable == nil {
		cfg.IsAvailable = func(eventschema.Provider, string) bool { return true }
	}
	return &Router{cfg: cfg, spend: spendEng}
}

// Kind reports the optimizer category.
func (r *Router) Kind() eventschema.OptimizationType { return eventschema.OptimizationTypeRouter }

// Run consults the routing table for req. Emits at most one recommendation.
func (r *Router) Run(_ context.Context, req *optimizer.Request) ([]optimizer.Recommendation, error) {
	if req == nil || req.Model == "" {
		return nil, nil
	}
	rule, ok := r.matchRule(req.Provider, req.Model)
	if !ok {
		return nil, nil
	}
	if rule.Quality < r.cfg.MinQuality {
		return nil, nil
	}
	// A pressure-scoped rule waits until the window is actually tight.
	var windowNote string
	if rule.WhenWindowPctAbove > 0 {
		pct, ok := r.windowPct(req.Provider)
		if !ok || pct < rule.WhenWindowPctAbove {
			return nil, nil
		}
		windowNote = fmt.Sprintf(" [window %.0f%% full, threshold %.0f%%]",
			pct, rule.WhenWindowPctAbove)
	}
	// A class-scoped rule only fires when the turn is confidently the
	// kind of work it was written for.
	var classNote string
	if rule.WhenClass != "" {
		sig := taskclass.Classify(req.Body, r.cfg.Classify)
		if string(sig.Class) != rule.WhenClass {
			return nil, nil
		}
		classNote = fmt.Sprintf(" [%s: %s]", sig.Class, sig.Reason)
	}
	target, ok := r.pickTarget(req.Provider, rule)
	if ok {
		// The operator's ceiling outranks the routing table: an upgrade
		// past their preferred model waits for their answer.
		if prop, needsApproval := r.upgradeCheck(req.Provider, req.Model, target); needsApproval {
			switch r.decisionFor(req.Provider, req.Model, target) {
			case DecisionApproved:
				// Fall through and route as configured.
			case DecisionDenied:
				return []optimizer.Recommendation{{
					Kind:         eventschema.OptimizationTypeRouter,
					Reason:       "router: upgrade declined by operator — " + prop.Reason,
					QualityScore: rule.Quality,
				}}, nil
			default:
				if r.cfg.OnProposal != nil {
					r.cfg.OnProposal(prop)
				}
				return []optimizer.Recommendation{{
					Kind:         eventschema.OptimizationTypeRouter,
					Reason:       "router: upgrade awaiting operator approval — " + prop.Reason,
					QualityScore: rule.Quality,
				}}, nil
			}
		}
	}
	if !ok {
		// Original target and all fallbacks unavailable; emit a skipped
		// rec so dashboards can flag the misroute.
		return []optimizer.Recommendation{{
			Kind:         eventschema.OptimizationTypeRouter,
			Reason:       fmt.Sprintf("router: no available target for %s", req.Model),
			QualityScore: rule.Quality,
		}}, nil
	}

	newBody, err := rewriteModel(req.Body, target)
	if err != nil {
		// Body unparseable / no model field — record a passive rec with
		// no ApplyBody so the pipeline still attributes the route.
		newBody = nil
	}
	tokenSavings, usdSavings := r.estimateSavings(req, target)

	return []optimizer.Recommendation{{
		Kind:                   eventschema.OptimizationTypeRouter,
		EstimatedSavingsTokens: tokenSavings,
		EstimatedSavingsUSD:    usdSavings,
		QualityScore:           rule.Quality,
		Reason:                 fmt.Sprintf("route %s -> %s%s%s", req.Model, target, windowNote, classNote),
		ApplyBody:              newBody,
	}}, nil
}

func (r *Router) matchRule(provider eventschema.Provider, model string) (Rule, bool) {
	for _, rule := range r.cfg.Rules {
		if rule.Provider != provider {
			continue
		}
		if rule.FromModel == model {
			return rule, true
		}
		if strings.HasSuffix(rule.FromModel, "*") {
			prefix := strings.TrimSuffix(rule.FromModel, "*")
			if strings.HasPrefix(model, prefix) {
				return rule, true
			}
		}
	}
	return Rule{}, false
}

func (r *Router) pickTarget(provider eventschema.Provider, rule Rule) (string, bool) {
	if rule.ToModel != "" && r.cfg.IsAvailable(provider, rule.ToModel) {
		return rule.ToModel, true
	}
	for _, fb := range rule.Fallbacks {
		if fb == "" {
			continue
		}
		if r.cfg.IsAvailable(provider, fb) {
			return fb, true
		}
	}
	return "", false
}

// estimateSavings reports the two independent currencies a route moves:
// the token volume it redirects, and the dollar delta that redirection
// buys. They are measured separately on purpose. Tokens are real
// whichever way the rate cards fall — a lateral or quality-motivated
// route still moved the whole request — and TEU sums this field, so
// gating it on a favourable price comparison silently starves the
// product's headline token metric.
//
// The dollar leg is floored at zero (a route to a pricier model is a
// deliberate trade, not a negative saving) and priced through the
// request's own CostSource, so a flat-rate subscription reports the
// $0.00 it will actually be billed instead of a list-price fiction.
func (r *Router) estimateSavings(req *optimizer.Request, target string) (int64, float64) {
	// Token volume is observable without a rate card, so it is reported
	// even when no spend engine is wired.
	tokens := max(req.InputTokens+req.OutputTokens, 0)
	if r.spend == nil {
		return tokens, 0
	}
	original := &eventschema.PromptEvent{
		Provider: req.Provider, RequestModel: req.Model,
		InputTokens: req.InputTokens, OutputTokens: req.OutputTokens,
		CostSource: req.CostSource,
	}
	rerouted := &eventschema.PromptEvent{
		Provider: req.Provider, RequestModel: target,
		InputTokens: req.InputTokens, OutputTokens: req.OutputTokens,
		CostSource: req.CostSource,
	}
	origCost, errA := r.spend.Compute(original)
	newCost, errB := r.spend.Compute(rerouted)
	if errA != nil || errB != nil {
		// Unpriced model on either side: the dollar leg is unknown, but
		// the token leg is not.
		return tokens, 0
	}
	usd := origCost - newCost
	if usd < 0 {
		usd = 0
	}
	return tokens, usd
}

// rewriteModel replaces the top-level "model" field in body with target
// and re-serialises. Returns ErrNoModelField when the body has no
// model key.
func rewriteModel(body []byte, target string) ([]byte, error) {
	if len(body) == 0 {
		return nil, ErrEmptyBody
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("router: parse body: %w", err)
	}
	if _, ok := raw["model"]; !ok {
		return nil, ErrNoModelField
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return nil, fmt.Errorf("router: encode target: %w", err)
	}
	out := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	out["model"] = encoded
	return json.Marshal(out)
}

// ErrEmptyBody / ErrNoModelField surface the two cases where Run cannot
// rewrite a request body and falls back to a passive recommendation.
var (
	ErrEmptyBody    = errors.New("router: empty body")
	ErrNoModelField = errors.New("router: no model field")
)

// decisionFor resolves the operator's standing answer for a route.
func (r *Router) decisionFor(provider eventschema.Provider, from, to string) Decision {
	if r.cfg.UpgradeDecision == nil {
		return DecisionPending
	}
	return r.cfg.UpgradeDecision(provider, from, to)
}

// errNoSpendEngine reports that no rate card is reachable, so two models
// cannot be ranked against each other.
var errNoSpendEngine = errors.New("router: no spend engine for rate comparison")

// windowPct reads the provider's rate-limit window fill, reporting
// whether the value can be trusted. An absent probe and an untrustworthy
// reading are the same answer to a caller: do not act.
func (r *Router) windowPct(provider eventschema.Provider) (float64, bool) {
	if r.cfg.WindowPressure == nil {
		return 0, false
	}
	return r.cfg.WindowPressure(provider)
}
