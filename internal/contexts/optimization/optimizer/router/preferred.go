package router

import (
	"fmt"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Decision is the operator's standing answer about one upgrade route.
type Decision string

// Decision values. Pending means nobody has answered yet.
const (
	DecisionPending  Decision = "pending"
	DecisionApproved Decision = "approved"
	DecisionDenied   Decision = "denied"
)

// Proposal is an upgrade the router refused to make on its own: a route
// that would move the operator to a model costing more than the one they
// said they prefer.
//
// It carries both candidates so whoever surfaces it can offer a real
// choice — take the proposed model, or stay on the preferred one — rather
// than just reporting that something was blocked.
type Proposal struct {
	Provider eventschema.Provider
	// FromModel is what the client asked for.
	FromModel string
	// ProposedModel is where the routing rule wanted to send it.
	ProposedModel string
	// PreferredModel is the operator's configured ceiling, offered as the
	// alternative they can accept instead.
	PreferredModel string
	// EstimatedDeltaUSD is what the upgrade would add per request at
	// list prices, or 0 when either side is unpriced.
	EstimatedDeltaUSD float64
	// Priced reports whether the comparison could be made at all. False
	// means a rate card was missing and the route was refused because it
	// could not be verified, not because it was measured as pricier.
	Priced bool
	// Reason explains the refusal in one line.
	Reason string
}

// Key identifies the route a Proposal is about, so a decision can be
// recorded against it and matched on later requests.
func (p Proposal) Key() string {
	return string(p.Provider) + "|" + p.FromModel + "|" + p.ProposedModel
}

// upgradeCheck decides whether a route needs the operator's blessing.
//
// The preferred model is a ceiling, not a pin: routing DOWN to something
// cheaper still applies automatically, because that is the saving the
// operator asked for. Only a move to something pricier than their
// preferred model is held back — that is a bill increase, and a quality
// trade, that they never agreed to.
//
// When either side has no rate card the comparison cannot be made, and
// the route is treated as an upgrade. Acting on an unverifiable change to
// someone's model is the failure this whole mechanism exists to prevent,
// so "cannot tell" resolves to "ask".
func (r *Router) upgradeCheck(provider eventschema.Provider, from, target string) (Proposal, bool) {
	if r.cfg.PreferredModel == nil {
		return Proposal{}, false
	}
	preferred := r.cfg.PreferredModel(provider)
	if preferred == "" || target == preferred {
		return Proposal{}, false
	}

	prop := Proposal{
		Provider:       provider,
		FromModel:      from,
		ProposedModel:  target,
		PreferredModel: preferred,
	}

	targetCost, errT := r.blendedRate(provider, target)
	preferredCost, errP := r.blendedRate(provider, preferred)
	switch {
	case errT != nil || errP != nil:
		prop.Reason = fmt.Sprintf(
			"cannot compare %s against your preferred %s — no rate card for one of them",
			target, preferred)
		return prop, true
	case targetCost > preferredCost:
		prop.Priced = true
		prop.EstimatedDeltaUSD = targetCost - preferredCost
		prop.Reason = fmt.Sprintf(
			"%s costs more than your preferred %s", target, preferred)
		return prop, true
	default:
		// Cheaper than or equal to the ceiling: apply it.
		return Proposal{}, false
	}
}

// blendedRate is a single comparable number per model: the cost of one
// million input tokens plus one million output tokens at list price. It
// exists only to rank two models against each other, so the absolute
// value is not meaningful on its own.
func (r *Router) blendedRate(provider eventschema.Provider, model string) (float64, error) {
	if r.spend == nil {
		return 0, errNoSpendEngine
	}
	c, err := r.spend.Compute(&eventschema.PromptEvent{
		Provider:     provider,
		RequestModel: model,
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
		// Deliberately metered: this is a list-price comparison between
		// two models, not a bill. Inheriting a plan's zero cost would
		// make every model compare equal and defeat the ceiling.
		CostSource: eventschema.CostSourceMetered,
	})
	if err != nil {
		return 0, err
	}
	return c, nil
}
