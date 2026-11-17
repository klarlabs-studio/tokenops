package boundaries

import (
	"fmt"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Trip is a boundary a task's quote would cross at its p90: the rule, what
// it requires and what would be crossed. It refuses and approves nothing;
// the approval, when one is needed, goes through the boundaries flow when
// the rule is met, and only on the operator's word.
type Trip struct {
	Rule    string `json:"rule"`
	Action  string `json:"action"`
	Require string `json:"require"`
	Crossed string `json:"crossed"`
	// Enforced is false for a rule in shadow: it records and binds
	// nothing.
	Enforced bool `json:"enforced"`
	// Already is true when the rule holds before the task starts.
	Already bool `json:"already"`
	// NeedsApproval is an enforced rule that asks someone; Refuses one
	// that denies.
	NeedsApproval bool   `json:"needs_approval"`
	Refuses       bool   `json:"refuses"`
	Advice        string `json:"advice"`
}

// QuoteTrips is every continue and spend rule a task would meet at its
// p90: the plan of provider as it would stand after the task (each
// window's share left less shares[w]) and the task's own cost, p90USD, as
// a session's. Budgets and the pace stand as they are now; a rule that
// needs a workflow, an agent, a folder or a loop cannot be judged before
// the work and is left out rather than guessed.
func QuoteTrips(rules []domain.Rule, env Env, reports []plans.HeadroomReport, provider string,
	shares map[domain.Window]float64, p90USD float64) []Trip {
	var judged []domain.Rule
	for _, r := range rules {
		if r.Require.Kind != domain.RequireAllow &&
			(r.Action == domain.ActionContinue || r.Action == domain.ActionSpend) && quoteJudgeable(r) {
			judged = append(judged, r)
		}
	}
	if len(judged) == 0 {
		return nil
	}
	now := env.now()
	figs := lazyFigures{dir: env.Dir, now: now}
	fig, _ := figs.get()
	plan := Figures(reports, nil, now)
	if len(figuresPlanLeft(plan, provider)) == 0 {
		plan = fig
	}
	early, basis := figuresPlanEarly(plan, provider)
	before := domain.Facts{PlanLeftPct: figuresPlanLeft(plan, provider), PlanEarlyMin: early, PlanEarlyBasis: basis,
		BudgetUsedPct: fig.BudgetUsedPct, Now: env.localNow()}
	after := before
	after.SessionCostUSD = domain.Known(p90USD)
	after.PlanLeftPct = map[domain.Window]float64{}
	for w, left := range before.PlanLeftPct {
		after.PlanLeftPct[w] = max(0, left-shares[w])
	}
	var out []Trip
	for _, r := range judged {
		m, ok := r.Match(r.Action, after)
		if !ok {
			continue
		}
		_, already := r.Match(r.Action, before)
		t := Trip{Rule: r.Name, Action: string(r.Action), Require: r.Require.String(), Crossed: m.Crossed(),
			Enforced: r.Enforced(), Already: already}
		switch {
		case !r.Enforced():
			t.Advice = "In shadow: TokenOps records that the rule would have applied and binds nothing."
		case r.Require.Kind == domain.RequireDeny:
			t.Refuses = true
			t.Advice = "The operator's boundary refuses work past this point. Tell them before starting; only they can lift it."
		default:
			t.NeedsApproval = true
			t.Advice = fmt.Sprintf("The operator's boundary needs %s past this point. Ask them before starting; "+
				"they answer through the boundaries flow, and an agent never approves its own.", approverWords(r.Require))
		}
		out = append(out, t)
	}
	return out
}

// quoteJudgeable reports whether the rule reads only figures a quote can
// project: the plan, the budgets, the hours and a session's cost.
func quoteJudgeable(r domain.Rule) bool {
	for _, c := range r.When {
		switch c.Field {
		case domain.FieldPlanLeft, domain.FieldPlanEarly, domain.FieldBudgetUsed, domain.FieldHours, domain.FieldSessionCost:
		default:
			return false
		}
	}
	return true
}

func approverWords(r domain.Require) string {
	if r.Kind == domain.RequireApprover {
		return "approval from " + r.Role
	}
	return "the operator's approval"
}
