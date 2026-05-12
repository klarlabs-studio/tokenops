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

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
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
	target, ok := r.pickTarget(req.Provider, rule)
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
		Reason:                 fmt.Sprintf("route %s -> %s", req.Model, target),
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

func (r *Router) estimateSavings(req *optimizer.Request, target string) (int64, float64) {
	if r.spend == nil {
		return 0, 0
	}
	original := &eventschema.PromptEvent{
		Provider: req.Provider, RequestModel: req.Model,
		InputTokens: req.InputTokens, OutputTokens: req.OutputTokens,
	}
	rerouted := &eventschema.PromptEvent{
		Provider: req.Provider, RequestModel: target,
		InputTokens: req.InputTokens, OutputTokens: req.OutputTokens,
	}
	origCost, errA := r.spend.Compute(original)
	newCost, errB := r.spend.Compute(rerouted)
	if errA != nil || errB != nil {
		return 0, 0
	}
	usd := origCost - newCost
	if usd <= 0 {
		// Routing to a more expensive model — savings reported as 0,
		// pipeline still records the recommendation.
		return 0, 0
	}
	// Token savings ≈ 0 (same prompt, same completion length); the win
	// is on $/token. Report the input/output token total as informational
	// so dashboards can show the routed-volume column.
	return req.InputTokens + req.OutputTokens, usd
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
