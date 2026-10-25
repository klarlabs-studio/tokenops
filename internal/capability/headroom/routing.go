package headroom

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Routing is the glance's advice to an agent while a plan is running
// short (plans.WindowPressure): pass a cheaper model on subagent calls for
// work that fits. It is for harnesses where TokenOps cannot rewrite the
// call itself; Claude Code's subagent hook moves the call on its own.
type Routing struct {
	Provider string          `json:"provider"`
	Pressure *plans.Pressure `json:"pressure"`
	// Advice is one line: why, and what to pass.
	Advice string `json:"advice"`
}

// routingVendors names each plan as its users do.
var routingVendors = map[string]string{"anthropic": "Claude", "openai": "Codex", "gemini": "Gemini", "github": "Copilot", "cursor": "Cursor"}

// RoutingAdvice is one line per plan under pressure, none when no plan is.
func RoutingAdvice(budgets []plans.SessionBudget, now time.Time) []Routing {
	var out []Routing
	seen := map[string]bool{}
	for _, b := range budgets {
		if seen[b.Provider] {
			continue
		}
		vendor := routingVendors[b.Provider]
		if vendor == "" {
			vendor = b.Display
		}
		p := plans.WindowPressure(vendor, b.Windows, now)
		if p == nil {
			continue
		}
		seen[b.Provider] = true
		what := "pass a cheaper `model` on subagent calls for work that fits, and keep the strongest model for deep work."
		if b.Provider == "anthropic" {
			what = "pass a cheaper `model` on Agent calls for work that fits: haiku for lookups, sonnet for research and routine edits; keep the strongest model for deep work."
		}
		out = append(out, Routing{Provider: b.Provider, Pressure: p, Advice: p.Why() + ": " + what})
	}
	return out
}
