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
	cat := in.Catalog.WithCandidates(in.Candidates)
	cur := cat.Resolve(in.Provider, from)
	if cur.Tier == modeltier.TierUnknown || tierRank[wantTier] >= tierRank[cur.Tier] {
		return d
	}
	target, ok := cat.Target(in.Provider, wantTier)
	if !ok {
		target, ok = cat.TargetBelow(in.Provider, cur.Tier)
	}
	alias := aliasFor(target)
	if !ok || target == from || alias == "" {
		return d
	}
	d.Rewrite, d.To, d.ToAlias = true, target, alias
	d.Reason = fmt.Sprintf("this subagent's work is %s; %s fits it and %s is on the %s tier", d.Kind, target, from, cur.Tier)
	return d
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
