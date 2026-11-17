package findings

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// clientNames names each client as its users do.
var clientNames = map[string]string{"claude-code": "Claude Code", "codex": "Codex", "opencode": "opencode", "cursor": "Cursor"}

func clientName(c string) string {
	if n, ok := clientNames[c]; ok {
		return n
	}
	return c
}

// effortFindings suggest a lower default effort for a model where the
// developer's own week shows a cheaper level did as well (the models
// power's advice). The setting is never written: the finding names the
// change, and the operator makes it or not.
func effortFindings(s *SessionsSnapshot, c *coach.Report) []Finding {
	if s == nil || (c != nil && c.Effective(config.PowerModels) == config.AutonomyOff) {
		return nil
	}
	var out []Finding
	for _, sg := range agentdx.SuggestEffort(s.DX.Metrics.ByEffort, SessionDays) {
		out = append(out, effortFinding(sg))
	}
	return out
}

func effortFinding(sg agentdx.EffortSuggestion) Finding {
	from, to := sg.From, sg.To
	f := Finding{
		Key:   key(KindCoach, "effort", sg.Client, sg.Model),
		Kind:  KindCoach,
		Level: LevelInfo,
		Title: fmt.Sprintf("%s in %s cost %.0f%% less per instruction at %s effort than at %s, with outcomes as good",
			sg.Model, clientName(sg.Client), sg.CostDropPct, to.Effort, from.Effort),
		Evidence: fmt.Sprintf("Last %d days. At %s: %s, first try %.0f%%, rejected %.0f%%, interrupted %.0f%%, %s each. "+
			"At %s: %s, first try %.0f%%, rejected %.0f%%, interrupted %.0f%%, %s each. %s",
			SessionDays,
			from.Effort, instructions(from.Instructions), from.FirstTryPct, from.RejectedPct, from.InterruptedPct, money(from.USDPerInstruction),
			to.Effort, instructions(to.Instructions), to.FirstTryPct, to.RejectedPct, to.InterruptedPct, money(to.USDPerInstruction),
			sg.Confound),
	}
	action := fmt.Sprintf("Consider %s as the default effort for %s", to.Effort, sg.Model)
	if change, ok := coach.EffortChange(sg.Client, sg.Model, to.Effort); ok {
		action += ": " + change
	} else {
		action += fmt.Sprintf(" in %s", clientName(sg.Client))
	}
	action += "."
	if sg.MonthlySavingUSD > 0 {
		action += fmt.Sprintf(" About %s a month at your volume (an estimate, at list prices).", money(sg.MonthlySavingUSD))
	}
	f.Action = action + " TokenOps does not change it for you; raise it again for work that needs it. `tokenops explain effort-suggestion`."
	return f
}

func instructions(n int) string { return fmt.Sprintf("%d %s", n, plural(n, "instruction")) }
