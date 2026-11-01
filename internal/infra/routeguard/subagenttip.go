package routeguard

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A subagent tip is the coach's note at a subagent launch while models is
// at advise: this kind of subagent, on this model, does work a cheaper one
// fits. A PreToolUse note reaches the agent with the launch it is about,
// so it speaks to the next launch of the same subagent type, and that
// launch settles it: on a model below the one the tip named is following
// it, on the same model or above is ignoring it.

// SubagentTip is a tip given at a subagent launch, waiting for the next
// launch of the same type.
type SubagentTip struct {
	ID string `json:"id"`
	// Type is the Agent tool's subagent_type ("Explore").
	Type string `json:"type"`
	// FromRank is the tier the subagent ran on when told; following the
	// tip is launching the next one below it.
	FromRank int `json:"from_rank"`
	// To is the model the tip suggested.
	To string `json:"to"`
}

// TipOutcome is what became of a subagent tip: followed, or ignored with
// what the agent launched instead.
type TipOutcome struct {
	ID       string
	Followed bool
	Instead  string
	Evidence string
}

// DefaultSubagentType is how a launch that names no subagent_type is
// referred to: Claude Code runs it as its general-purpose agent.
const DefaultSubagentType = "general-purpose"

// SubagentType is the launch's subagent type, DefaultSubagentType when it
// names none.
func SubagentType(toolInput map[string]any) string {
	t, _ := toolInput["subagent_type"].(string)
	if t = strings.TrimSpace(t); t == "" {
		return DefaultSubagentType
	}
	return t
}

// ModelRank is the tier rank of model, an Agent-tool alias or an id, on
// the menu candidates offers; 0 when it cannot be placed.
func ModelRank(model string, provider eventschema.Provider, catalog *modeltier.Catalog, candidates []string) int {
	if catalog == nil || model == "" {
		return 0
	}
	resolved := resolveAlias(model, candidates)
	return tierRank[catalog.WithCandidates(withCurrent(candidates, resolved)).Resolve(provider, resolved).Tier]
}

// OpenSubagentTip keeps a tip given at a launch until the next launch of
// its type settles it. A newer tip for the same type replaces an older.
func OpenSubagentTip(dir, session string, tip SubagentTip) {
	if dir == "" || tip.ID == "" {
		return
	}
	st := loadState(dir, session)
	kept := st.Tips[:0]
	for _, t := range st.Tips {
		if t.Type != tip.Type {
			kept = append(kept, t)
		}
	}
	st.Tips = append(kept, tip)
	saveState(dir, session, st)
}

// SettleSubagentTips settles the open tips for a launch of subagentType
// on model (what the agent asked for, or the session's model when it
// inherits), ranked rank (ModelRank). A model that cannot be placed
// settles nothing.
func SettleSubagentTips(dir, session, subagentType, model string, rank int) []TipOutcome {
	if dir == "" || rank == 0 {
		return nil
	}
	st := loadState(dir, session)
	if len(st.Tips) == 0 {
		return nil
	}
	var out []TipOutcome
	kept := st.Tips[:0]
	for _, t := range st.Tips {
		if t.Type != subagentType {
			kept = append(kept, t)
			continue
		}
		name := aliasFor(model)
		if name == "" {
			name = model
		}
		if rank < t.FromRank {
			out = append(out, TipOutcome{ID: t.ID, Followed: true, Evidence: "the next " + t.Type + " subagent ran on " + name})
			continue
		}
		out = append(out, TipOutcome{ID: t.ID, Instead: "launched " + t.Type + " on " + name + " again"})
	}
	if len(out) > 0 {
		st.Tips = kept
		saveState(dir, session, st)
	}
	return out
}
