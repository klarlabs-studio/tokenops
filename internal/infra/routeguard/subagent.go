package routeguard

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SubagentInput is one subagent launch the agent is about to make.
//
// A hook cannot change the session's own model, but Claude Code's Agent
// tool carries the subagent's model in its input and a PreToolUse hook can
// rewrite it (verified in a real session, ADR 0006). That is the coach's
// autonomous move for models on the hook path.
type SubagentInput struct {
	// Description is the Agent tool's short label for the work. It reads
	// like a person's request and is classified first.
	Description string
	// Prompt is the work handed to the subagent, used when the
	// description says nothing classifiable.
	Prompt string
	// Requested is the model the agent asked for ("haiku", "opus", or an
	// id); empty means the subagent inherits SessionModel.
	Requested    string
	SessionModel string
	Provider     eventschema.Provider
	Catalog      *modeltier.Catalog
	Candidates   []string
	// Pressure is the Claude plan running short (ReadPressure), or nil.
	// It moves edit and deep work one tier further down; with none,
	// placement is by the work alone.
	Pressure *Pressure
	// Retracted reports whether moving kind work from one model family to
	// another has been retracted because its quality dropped
	// (moveverdict); such a move is not made. Nil retracts nothing.
	Retracted func(kind, from, to string) bool
}

// SubagentDecision says whether to move the subagent and where.
type SubagentDecision struct {
	Rewrite bool
	Kind    taskclass.Kind
	From    string
	To      string
	// ToAlias is how the Agent tool names To.
	ToAlias string
	Reason  string
	// Pressure is the evidence that moved the subagent further than its
	// work alone would have; nil when the work alone placed it.
	Pressure *Pressure
	// Held names a move that would have been made but was retracted
	// because its quality dropped, "" when none was.
	Held string
}

// agentAliases are the model names the Agent tool accepts.
var agentAliases = []string{"haiku", "sonnet", "opus"}

// EvaluateSubagent decides whether a subagent should run on a cheaper model
// that fits its work. It only ever moves down, and abstains whenever the
// work, the model, or the target cannot be placed with confidence.
func EvaluateSubagent(in SubagentInput) SubagentDecision {
	d := SubagentDecision{}
	if in.Catalog == nil || len(in.Candidates) == 0 {
		return d
	}
	d.Kind = taskclass.KindForTurn(in.Description, "")
	if d.Kind == "" {
		d.Kind = taskclass.KindForTurn(in.Prompt, "")
	}
	wantTier, ok := tierForKind(d.Kind)
	if !ok {
		return d
	}
	from := in.SessionModel
	if r := strings.TrimSpace(in.Requested); r != "" {
		from = resolveAlias(r, in.Candidates)
	}
	d.From = from
	cat := in.Catalog.WithCandidates(withCurrent(in.Candidates, from))
	cur := cat.Resolve(in.Provider, from)
	if cur.Tier == modeltier.TierUnknown {
		return d
	}
	calm, calmOK := placeSubagent(cat, in.Provider, cur.Tier, wantTier, from, false)
	if calmOK && d.retracted(in, from, calm) {
		calmOK = false
	}
	if pressed, ok := pressedTier(d.Kind); ok && in.Pressure != nil {
		// Placed exactly: a pressed tier the menu cannot fill is no
		// reason to fall further than one step.
		if target, ok := placeSubagent(cat, in.Provider, cur.Tier, pressed, from, true); ok && (!calmOK || target != calm) && !d.retracted(in, from, target) {
			d.Rewrite, d.To, d.ToAlias, d.Pressure = true, target, aliasFor(target), in.Pressure
			d.Reason = fmt.Sprintf("%s; %s work moved from %s to %s", in.Pressure.Why(), d.Kind, from, target)
			return d
		}
	}
	if !calmOK {
		return d
	}
	d.Rewrite, d.To, d.ToAlias = true, calm, aliasFor(calm)
	d.Reason = fmt.Sprintf("this subagent's work is %s; %s fits it and %s is on the %s tier", d.Kind, calm, from, cur.Tier)
	return d
}

// retracted reports whether moving this kind of work from from to to has
// been retracted, and notes the held move on the decision.
func (d *SubagentDecision) retracted(in SubagentInput, from, to string) bool {
	if in.Retracted == nil || !in.Retracted(string(d.Kind), aliasFor(from), aliasFor(to)) {
		return false
	}
	if d.Held == "" {
		d.Held = fmt.Sprintf("moving %s subagents from %s to %s was retracted because its quality dropped", d.Kind, aliasFor(from), aliasFor(to))
	}
	return true
}

// placeSubagent is the model on want's tier for a subagent now on cur, or
// false when that would not move it down to a model the Agent tool can
// name. Unless exact, a menu without want's tier offers the next tier
// below cur instead.
func placeSubagent(cat *modeltier.Catalog, provider eventschema.Provider, cur, want modeltier.Tier, from string, exact bool) (string, bool) {
	if tierRank[want] >= tierRank[cur] {
		return "", false
	}
	target, ok := cat.Target(provider, want)
	if !ok && !exact {
		target, ok = cat.TargetBelow(provider, cur)
	}
	if !ok || target == from || aliasFor(target) == "" {
		return "", false
	}
	return target, true
}

// pressedTier is the tier subagent work wants while the plan is pressed:
// one step down for edits and deep work. Lookups and research already
// sit on what they need and stay there.
func pressedTier(k taskclass.Kind) (modeltier.Tier, bool) {
	switch k {
	case taskclass.KindEdit:
		return modeltier.TierBalanced, true
	case taskclass.KindDeep:
		return modeltier.TierDefault, true
	}
	return modeltier.TierUnknown, false
}

// resolveAlias maps an Agent-tool alias to the offered model it names; an
// id passes through.
func resolveAlias(requested string, candidates []string) string {
	r := strings.ToLower(requested)
	for _, a := range agentAliases {
		if r != a {
			continue
		}
		for _, c := range candidates {
			if strings.Contains(strings.ToLower(c), a) {
				return c
			}
		}
	}
	return requested
}

// aliasFor is the Agent-tool alias for a model id, or "" when the tool has
// no name for it.
func aliasFor(model string) string {
	m := strings.ToLower(model)
	for _, a := range agentAliases {
		if strings.Contains(m, a) {
			return a
		}
	}
	return ""
}
