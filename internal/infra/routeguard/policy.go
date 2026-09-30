package routeguard

import (
	"fmt"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/governance/modelpolicy"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// PolicyInput is one subagent launch checked against the model policy.
type PolicyInput struct {
	// Requested is the model the agent asked for; empty means the
	// subagent inherits SessionModel.
	Requested    string
	SessionModel string
	Provider     eventschema.Provider
	Catalog      *modeltier.Catalog
	// Offered is every model on offer, forbidden ones included, so an
	// alias such as "opus" resolves to the model it names before the
	// policy rules on it.
	Offered []string
	Policy  modelpolicy.Policy
}

// PolicyDecision says whether a subagent would run on a forbidden model
// and, if so, where it goes instead.
type PolicyDecision struct {
	Forbidden bool
	From      string
	// To is the closest permitted model, and ToAlias how the Agent tool
	// names it. Both are empty when nothing permitted can take the work,
	// and the call is refused.
	To      string
	ToAlias string
	Reason  string
}

// EnforcePolicy keeps a subagent off models the operator ruled out. A
// forbidden model is replaced by the permitted one closest to it — the
// same tier first, then the nearest below, then the nearest above — so
// the work keeps as much of the capability it asked for as the policy
// allows.
func EnforcePolicy(in PolicyInput) PolicyDecision {
	if in.Policy.Empty() {
		return PolicyDecision{}
	}
	from := in.SessionModel
	if r := strings.TrimSpace(in.Requested); r != "" {
		from = resolveAlias(r, in.Offered)
	}
	v := in.Policy.Check(in.Provider, from)
	if v.Permitted {
		return PolicyDecision{}
	}
	d := PolicyDecision{Forbidden: true, From: from}
	permitted := in.Policy.Filter(in.Provider, in.Offered)
	if to, ok := closestPermitted(in, from, permitted); ok {
		d.To, d.ToAlias = to, aliasFor(to)
		d.Reason = fmt.Sprintf("%s is ruled out by your model policy (%s); %s is the closest permitted model", from, v.Reason, to)
		return d
	}
	d.Reason = fmt.Sprintf("%s is ruled out by your model policy (%s) and no permitted model can take this subagent", from, v.Reason)
	if len(permitted) > 0 {
		d.Reason += "; permitted: " + strings.Join(permitted, ", ")
	}
	return d
}

// closestPermitted ranks the permitted models the Agent tool can name by
// their tier's distance from the forbidden one.
func closestPermitted(in PolicyInput, from string, permitted []string) (string, bool) {
	rankOf := func(t modeltier.Tier) int {
		if r, ok := tierRank[t]; ok {
			return r
		}
		// An unplaced model is treated as the everyday default.
		return tierRank[modeltier.TierDefault]
	}
	var cat *modeltier.Catalog
	if in.Catalog != nil {
		cat = in.Catalog.WithCandidates(withCurrent(permitted, from))
	}
	tierOf := func(m string) modeltier.Tier {
		if cat == nil {
			return modeltier.TierUnknown
		}
		return cat.Resolve(in.Provider, m).Tier
	}
	want := rankOf(tierOf(from))
	type option struct {
		model    string
		distance int
	}
	var options []option
	for _, m := range permitted {
		if aliasFor(m) == "" {
			continue
		}
		diff := rankOf(tierOf(m)) - want
		distance := -diff * 2 // below: 2, 4, 6
		if diff > 0 {
			distance = diff*2 + 1 // above: 3, 5, 7, after the same distance below
		}
		options = append(options, option{m, distance})
	}
	if len(options) == 0 {
		return "", false
	}
	sort.SliceStable(options, func(i, j int) bool { return options[i].distance < options[j].distance })
	return options[0].model, true
}
