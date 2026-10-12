package boundaries

import (
	"errors"
	"regexp"
	"strings"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The decision points inside TokenOps (ADR 0014 §3): the router, which
// may move a request to another model, and the uploader and exporter,
// which send figures off the machine. Both can genuinely refuse — the
// router by leaving the request on its model, the senders by not sending
// — so their rules are enforced, not advice.

// Destinations a share rule names, for the senders.
const (
	DestinationTeam = domain.DestinationTeam
	DestinationOTel = domain.DestinationOTel
)

var unsafeScope = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// scope names the "session" a TokenOps-side decision belongs to, so its
// id is stable and an answer applies to it alone: one per route, one per
// destination. It is safe as a file name.
func scope(parts ...string) string {
	for i, p := range parts {
		parts[i] = unsafeScope.ReplaceAllString(p, "_")
	}
	return strings.Join(parts, "-")
}

// RouteCheck is the router's model_route boundary: asked before a route
// is applied, it reports whether the route is held and why. rules is
// read on every call, so organisation rules fetched since apply; with no
// model_route rule the answer costs a loop over the rules.
//
// A route is its own decision, per provider and model pair, so an
// approval lets that route through: once, or for 24 hours with
// --session. An evaluation that fails lets the route through and is
// recorded (fail open).
func RouteCheck(rules func() []Rule, env Env) func(provider eventschema.Provider, from, to, workflowID, agentID string) (bool, string) {
	return func(provider eventschema.Provider, from, to, workflowID, agentID string) (bool, string) {
		relevant := forAction(rules(), domain.ActionModelRoute)
		if len(relevant) == 0 {
			return false, ""
		}
		call := Call{
			Harness: PointRouter, SessionID: scope("route", string(provider), from, to),
			Action: domain.ActionModelRoute, Model: to, Provider: string(provider),
			AgentID: agentID, WorkflowID: workflowID,
		}
		res, err := Guard(relevant, call, env)
		if err != nil {
			RecordFailure(call, env, err)
			return false, ""
		}
		return res.Verdict != VerdictNone, res.Reason
	}
}

// ShareRefusedError is figures held by a share boundary.
type ShareRefusedError struct {
	Destination string
	DecisionID  string
	Reason      string
}

func (e *ShareRefusedError) Error() string { return e.Reason }

// IsShareRefused reports whether err is a share boundary holding figures.
func IsShareRefused(err error) bool {
	var refused *ShareRefusedError
	return errors.As(err, &refused)
}

// CheckShare is the share boundary, asked before figures leave for
// destination (team or otel). It returns a *ShareRefusedError when a rule
// holds them, and nil otherwise; an evaluation that fails lets them go
// and is recorded (fail open), like every other point.
func CheckShare(rules []domain.Rule, destination string, env Env) error {
	relevant := forAction(rules, domain.ActionShare)
	if len(relevant) == 0 {
		return nil
	}
	call := Call{
		Harness: PointTokenOps, SessionID: scope("share", destination), Action: domain.ActionShare,
		Destination: destination, AgentID: PointTokenOps,
	}
	res, err := Guard(relevant, call, env)
	if err != nil {
		RecordFailure(call, env, err)
		return nil
	}
	if res.Verdict == VerdictNone {
		return nil
	}
	return &ShareRefusedError{Destination: destination, DecisionID: res.DecisionID, Reason: res.Reason}
}

// NeedsFigures reports whether any rule compares a figure only the daemon
// can measure — plan headroom outside Claude Code's status line, or a
// budget — so the daemon writes them only when something reads them.
func NeedsFigures(rules []domain.Rule) bool {
	return needs(rules, domain.FieldPlanLeft) || needs(rules, domain.FieldBudgetUsed)
}

// Figures builds the readings the daemon writes for the pause point: the
// share of each provider's 5-hour and weekly window left, from the
// glance's reports, and each budget's share used. A stale or reset window
// is left out; a missing figure never satisfies a rule.
func Figures(reports []plans.HeadroomReport, budgetUsed map[string]float64, now time.Time) boundaryledger.Figures {
	out := boundaryledger.Figures{ObservedAt: now.UTC(), BudgetUsedPct: budgetUsed}
	for _, r := range reports {
		for _, w := range r.Windows {
			name := windowName(w)
			if name == "" || w.Stale || (!w.ResetsAt.IsZero() && w.ResetsAt.Before(now)) {
				continue
			}
			if out.PlanLeftPct == nil {
				out.PlanLeftPct = map[string]map[string]float64{}
			}
			byWindow := out.PlanLeftPct[r.Provider]
			if byWindow == nil {
				byWindow = map[string]float64{}
				out.PlanLeftPct[r.Provider] = byWindow
			}
			left := max(0, 100-w.UsedPct)
			// Two plans on one provider: the tighter one binds.
			if cur, ok := byWindow[name]; !ok || left < cur {
				byWindow[name] = left
			}
		}
	}
	return out
}

// windowName maps a vendor window onto the two a rule can name. A window
// scoped to one model ("week (Fable)") is left out: it does not bound the
// plan as a whole.
func windowName(w plans.VendorWindow) string {
	switch w.Name {
	case "5h":
		return string(domain.Window5h)
	case "week":
		return string(domain.WindowWeek)
	}
	return ""
}
