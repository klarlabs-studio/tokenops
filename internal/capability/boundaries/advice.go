package boundaries

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Advice is an enforced boundary that holds now, told to an agent whose
// harness cannot be refused (Codex, Cursor) through the glance it reads
// before work (ADR 0014 §3). It refuses nothing; it says what the
// operator set and what to do about it.
type Advice struct {
	Rule    string `json:"rule"`
	Action  string `json:"action"`
	Require string `json:"require"`
	Crossed string `json:"crossed"`
	Advice  string `json:"advice"`
}

// AdviceNow is every enforced continue and spend rule that holds on the
// figures TokenOps has without a session: plan headroom and budgets. A
// rule that needs the session's cost, or a workflow or agent, cannot be
// judged here and is left out rather than guessed. Plan headroom is the
// daemon's figure when fresh, otherwise the glance's own reports, and
// either way the tightest plan known: the reader's provider is not.
func AdviceNow(rules []domain.Rule, env Env, reports []plans.HeadroomReport) []Advice {
	var judged []domain.Rule
	for _, r := range rules {
		if r.Enforced() && r.Require.Kind != domain.RequireAllow &&
			(r.Action == domain.ActionContinue || r.Action == domain.ActionSpend) && sessionFree(r) {
			judged = append(judged, r)
		}
	}
	if len(judged) == 0 {
		return nil
	}
	now := env.now()
	figs := lazyFigures{dir: env.Dir, now: now}
	fig, _ := figs.get()
	plan := fig
	if len(plan.PlanLeftPct) == 0 {
		plan = Figures(reports, nil, now)
	}
	facts := domain.Facts{PlanLeftPct: figuresPlanLeft(plan, ""), BudgetUsedPct: fig.BudgetUsedPct}
	var out []Advice
	for _, r := range judged {
		m, ok := r.Match(r.Action, facts)
		if !ok {
			continue
		}
		out = append(out, Advice{
			Rule: r.Name, Action: string(r.Action), Require: r.Require.String(), Crossed: m.Crossed(),
			Advice: adviceFor(r.Require),
		})
	}
	return out
}

// sessionFree reports whether the rule reads only figures known outside a
// session.
// GlanceAdvice is AdviceNow for a glance, with the rules from cfg; nil
// cfg advises nothing.
func GlanceAdvice(cfg *config.Config, reports []plans.HeadroomReport) []Advice {
	if cfg == nil {
		return nil
	}
	return AdviceNow(AllRules(*cfg), Env{}, reports)
}

func sessionFree(r domain.Rule) bool {
	for _, c := range r.When {
		switch c.Field {
		case domain.FieldPlanLeft, domain.FieldBudgetUsed:
		default:
			return false
		}
	}
	return true
}

func adviceFor(r domain.Require) string {
	switch r.Kind {
	case domain.RequireDeny:
		return "The operator's boundary refuses further work while this holds. Do not start new work: stop and tell them."
	case domain.RequireApprover:
		return fmt.Sprintf("The operator's boundary needs approval from %s past this point. Stop and ask before going on.", r.Role)
	default:
		return "The operator asked to be consulted past this point. Stop and ask before going on."
	}
}
