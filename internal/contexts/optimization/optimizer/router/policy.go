package router

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A routing rule is a decision the operator made once, in advance, about
// a pair of model names. That is a reasonable thing to be able to write
// and a poor thing to have to write: the rule cannot see the turn it is
// deciding about, so it is either too broad to be safe or too narrow to
// fire, and either way it goes stale the moment a model is renamed.
//
// Policy is the same decision made per turn, from signal that is actually
// measured: what kind of work this turn is, how much of the plan's window
// is already gone, and what the pricing table currently says is cheapest.
// Nobody writes a from/to pair, so nobody has to maintain one.
//
// It is deliberately narrow. Conserving only applies to work the
// classifier is confident is mechanical, only while the window is
// genuinely tight, and only downwards — the preferred-model ceiling still
// outranks it, so the policy can never raise a bill. Every one of those
// is a case where abstaining costs a missed saving and acting costs the
// operator a quality trade they did not ask for, and those are not
// symmetric.
type Policy struct {
	// Enabled turns the rules-free policy on. Off by default: the
	// behaviour before it existed is that an unmatched request is left
	// alone, and an optimizer that starts rewriting models on upgrade is
	// the opposite of what a ceiling is for.
	Enabled bool
	// WindowPctAbove is how full the plan's rate-limit window must be
	// before the policy conserves anything. Zero takes
	// DefaultPolicyWindowPct.
	//
	// There is no "conserve always" setting. On a flat-rate plan a
	// request costs nothing at the margin, so routing down while there is
	// headroom trades quality for a saving that does not exist.
	WindowPctAbove float64
	// Quality is the confidence that a mechanical turn survives the
	// cheapest model, gated by Config.MinQuality like any rule. Zero
	// takes DefaultPolicyQuality.
	Quality float64
}

// Policy defaults.
//
// 70% is where the plan's own meter stops being a curiosity: below it an
// operator who conserves is giving up quality for headroom they were
// never going to need.
//
// 0.75 sits just above the router's default MinQuality of 0.7, so the
// policy is on the right side of the gate by a margin rather than by
// coincidence — and an operator who raises MinQuality past it switches
// the policy off, which is the correct reading of that setting.
const (
	DefaultPolicyWindowPct = 70.0
	DefaultPolicyQuality   = 0.75
)

func (p Policy) withDefaults() Policy {
	if p.WindowPctAbove <= 0 {
		p.WindowPctAbove = DefaultPolicyWindowPct
	}
	if p.Quality <= 0 {
		p.Quality = DefaultPolicyQuality
	}
	return p
}

// Advice is a per-turn routing verdict that has not been applied to
// anything. It is what the policy decided and, when it decided to do
// nothing, why — an abstention with no reason attached is
// indistinguishable from a broken optimizer.
type Advice struct {
	// Model is where the turn should go. Empty means stay where you are.
	Model string
	// Stay is true when the answer is to leave the model alone. It is
	// not the negation of a useful Model: staying is a real answer and
	// the reason for it is the useful part.
	Stay bool
	// Reason explains the verdict in one line, naming the signal it was
	// made from.
	Reason string
	// Class is what the classifier made of the turn, or "unknown" when
	// it declined to call it.
	Class string
	// WindowPct is how full the plan window was, and WindowKnown whether
	// that reading exists at all. An unmeasured window is not a full one.
	WindowPct   float64
	WindowKnown bool
	// Quality is the confidence attached to a recommendation to move.
	Quality float64
}

// AdviceInput is one turn, described by whoever is asking.
type AdviceInput struct {
	Provider eventschema.Provider
	// Model is what the turn would otherwise run on.
	Model string
	// Instruction is the operator's latest instruction. Empty makes the
	// classifier abstain, which makes the policy abstain.
	Instruction string
	// ToolDensity is the share of the exchange that is tool traffic, when
	// the caller can measure it. Zero only ever makes the verdict more
	// conservative.
	ToolDensity float64
}

// Advise answers "what should this turn run on" without changing
// anything.
//
// This is the whole non-proxy path. Routing enforces by rewriting the
// model in the request, which needs the proxy, which needs a base-URL
// override most operators never wire — so on most machines the optimizer
// has an opinion nobody can hear. Advice is that same opinion, reachable
// by an agent that can call a tool, which is every client that speaks
// MCP.
//
// It runs the same policy as the request path, deliberately: advice that
// disagreed with enforcement would be worse than no advice.
func (r *Router) Advise(in AdviceInput) Advice {
	pol := r.cfg.Policy.withDefaults()
	sig := taskclass.ClassifyTurn(in.Instruction, in.ToolDensity, r.cfg.Classify)
	adv := Advice{Stay: true, Class: string(sig.Class)}

	if !r.cfg.Policy.Enabled {
		adv.Reason = "smart routing is off; only the configured rules apply"
		return adv
	}
	if in.Model == "" {
		adv.Reason = "no model named, so there is nothing to compare against"
		return adv
	}

	pct, known := r.windowPct(in.Provider)
	adv.WindowPct, adv.WindowKnown = pct, known

	if sig.Class != taskclass.Mechanical {
		adv.Reason = fmt.Sprintf("stay on %s — %s", in.Model, sig.Reason)
		return adv
	}
	if !known {
		// An unmeasured window is not a full one. Conserving on the
		// strength of a meter that is not reporting is acting on a
		// shortage that may not exist.
		adv.Reason = fmt.Sprintf("stay on %s — mechanical work, but the plan window is not being measured", in.Model)
		return adv
	}
	if pct < pol.WindowPctAbove {
		adv.Reason = fmt.Sprintf(
			"stay on %s — window %.0f%% full, below the %.0f%% at which conserving is worth a quality trade",
			in.Model, pct, pol.WindowPctAbove)
		return adv
	}

	target, ok := r.cheapestTarget(in.Provider)
	switch {
	case !ok:
		adv.Reason = fmt.Sprintf("stay on %s — no priced alternative for %s", in.Model, in.Provider)
		return adv
	case target == in.Model:
		adv.Reason = fmt.Sprintf("stay on %s — already the cheapest priced model for %s", in.Model, in.Provider)
		return adv
	}
	// The ceiling outranks the policy in both directions: it can never
	// propose something pricier than the operator's preferred model, and
	// it does not need their blessing to go cheaper.
	if _, needsApproval := r.upgradeCheck(in.Provider, in.Model, target); needsApproval {
		adv.Reason = fmt.Sprintf("stay on %s — %s is not cheaper than your preferred model", in.Model, target)
		return adv
	}
	if pol.Quality < r.cfg.MinQuality {
		adv.Reason = fmt.Sprintf(
			"stay on %s — the policy's confidence (%.2f) is below your quality floor (%.2f)",
			in.Model, pol.Quality, r.cfg.MinQuality)
		return adv
	}

	return Advice{
		Model:       target,
		Class:       string(sig.Class),
		WindowPct:   pct,
		WindowKnown: true,
		Quality:     pol.Quality,
		Reason: fmt.Sprintf("%s -> %s — %s, and the window is %.0f%% full",
			in.Model, target, sig.Reason, pct),
	}
}

// policyRule synthesises the rule the operator did not have to write, for
// the request path. It returns the same verdict Advise would give, shaped
// so the rest of Run — availability, the ceiling, proposals, savings —
// handles it exactly like a configured rule.
func (r *Router) policyRule(provider eventschema.Provider, model string, body []byte) (Rule, bool) {
	if !r.cfg.Policy.Enabled {
		return Rule{}, false
	}
	pol := r.cfg.Policy.withDefaults()
	sig := taskclass.Classify(body, r.cfg.Classify)
	if sig.Class != taskclass.Mechanical {
		return Rule{}, false
	}
	target, ok := r.cheapestTarget(provider)
	if !ok || target == model {
		return Rule{}, false
	}
	return Rule{
		Provider:  provider,
		FromModel: model,
		ToModel:   target,
		Quality:   pol.Quality,
		// Expressed as rule scopes rather than checked here, so the
		// window reading and the class check happen in one place for
		// configured and policy routes alike.
		WhenWindowPctAbove: pol.WindowPctAbove,
		WhenClass:          string(taskclass.Mechanical),
	}, true
}

// cheapestTarget is the cheapest model a request can actually name.
//
// The rate card prices most families by prefix ("gpt-4o-mini*",
// "claude-haiku-4-5*"), which is the right way to hold prices and the
// wrong thing to put in a request: no API accepts a literal asterisk. A
// trailing "*" is trimmed, which yields the family alias the vendor
// itself publishes; a wildcard anywhere else is not a name that can be
// recovered, so those rows are skipped.
//
// Cheapest is not the same as best, and this is the honest limit of a
// policy with no rules in it: if a provider's rate card ever lists a
// cheap model nobody should route to, the policy will pick it. The gates
// around it are what make that tolerable — mechanical work only, a tight
// window only, never past the operator's ceiling, and the quality floor
// on top. An operator who wants a specific target still writes a rule,
// and a rule always wins.
func (r *Router) cheapestTarget(provider eventschema.Provider) (string, bool) {
	if r.spend == nil {
		return "", false
	}
	var (
		best     string
		bestCost = -1.0
	)
	for k, rate := range r.spend.Table().Rates {
		if k.Provider != provider {
			continue
		}
		name, ok := nameable(k.Model)
		if !ok {
			continue
		}
		cost := rate.InputPerMillion + rate.OutputPerMillion
		if cost <= 0 {
			continue
		}
		if bestCost < 0 || cost < bestCost || (cost == bestCost && name < best) {
			bestCost, best = cost, name
		}
	}
	return best, best != ""
}

// nameable turns a rate-card key into something a request can carry, or
// reports that it cannot.
func nameable(model string) (string, bool) {
	trimmed := strings.TrimSuffix(model, "*")
	if trimmed == "" || strings.Contains(trimmed, "*") {
		return "", false
	}
	return trimmed, true
}
