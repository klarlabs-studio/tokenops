package coach

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
)

// Reach says whether autopilot has anywhere to act on this machine. Its
// moves on a re-read and on a subagent or default model happen through
// hooks in the agent; without them they never happen, and "nothing done"
// would read as "nothing needed". Only Status fills it, and only when the
// waste or models power is autonomous.
type Reach struct {
	// Acts is true when an autonomous power has a hook to act through.
	Acts bool `json:"acts"`
	// Why says, when Acts is false, which hooks are missing and what each
	// one stops.
	Why string `json:"why,omitempty"`
	// Fix is the command that wires the missing hooks, "" when no agent is
	// set up here to wire them into.
	Fix string `json:"fix,omitempty"`
}

// hookFix is the install that wires every hook autopilot acts through into
// one agent.
const hookFix = "tokenops hooks install --read-guard --coach --route-guard --client "

// reachOf works out Reach from the powers, the hooks wired and the
// compaction settings. Compaction is a setting the agent reads, not a hook,
// and its moves do not show in the activity the coach reports, so it does
// not count as an act here.
func reachOf(powers []Power, w Wiring, _ []LeverResult) *Reach {
	auto := map[string]bool{}
	for _, p := range powers {
		if p.Effective == config.AutonomyAutonomous {
			auto[p.Name] = true
		}
	}
	wastes, models := auto[config.PowerWaste], auto[config.PowerModels]
	if !wastes && !models {
		return nil
	}
	var reasons []string
	acts, needsHooks := false, false
	if wastes {
		if w.ClaudeCode.ReadGuard || w.Opencode.ReadGuard {
			acts = true
		} else {
			needsHooks = true
			reasons = append(reasons, "refusing re-reads needs the read guard in Claude Code or opencode")
		}
	}
	if models {
		if w.ClaudeCode.RouteGuard || w.ClaudeCode.Coach {
			acts = true
		} else {
			needsHooks = true
			reasons = append(reasons, "moving subagents and the default model needs the route guard and the coach hook in Claude Code")
		}
	}
	if acts {
		return &Reach{Acts: true}
	}
	r := &Reach{Why: "Autopilot has nowhere to act on this machine: " + strings.Join(reasons, "; ") + "."}
	switch {
	case w.ClaudeCode.Present:
		r.Fix = hookFix + "claude-code"
	case w.Opencode.Present:
		r.Fix = hookFix + "opencode"
	case needsHooks:
		r.Why += " No Claude Code or opencode config was found here, so there is no agent to wire them into."
	}
	return r
}
