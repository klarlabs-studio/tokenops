package coach

import (
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/modellever"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// DefaultModelKind names the default-model move and advice in the
// follow-through ledger.
const DefaultModelKind = "default_model"

// DefaultModelEnv is what reconciling the default model reads.
type DefaultModelEnv struct {
	Paths modellever.Paths
	// Rung is the models power's effective rung; Verbosity the coach's.
	Rung, Verbosity string
	// Pressure is the plan running short, nil when it is not or when no
	// fresh reading says (Known false).
	Pressure *routeguard.Pressure
	Known    bool
	// Session and SessionModel are the session whose turn just ended.
	Session, SessionModel string
	Catalog               *routeguard.Catalog
	// Candidates are the offered models, already filtered by the model
	// policy.
	Candidates []string
	Ledger     Ledger
	Now        time.Time
	// Retracted reports whether a move kind (task kind, from-family,
	// to-family) was retracted because its quality dropped
	// (VerifyMoves). A retracted default-model move is not made, and one
	// in place goes; the coach advises instead. Nil retracts nothing.
	Retracted func(kind, from, to string) bool
}

// retracted reports whether starting sessions on to instead of from is a
// retracted move.
func (e DefaultModelEnv) retracted(from, to string) bool {
	return e.Retracted != nil && e.Retracted(DefaultModelKind, routeguard.AliasFor(from), routeguard.AliasFor(to))
}

// ReconcileDefaultModel keeps Claude Code's default model for new sessions
// in step with the plan's pressure (models: autonomous): one tier down
// while the plan is pressed, the operator's own default again once it is
// not, or once the power is lowered. Below autonomous, and wherever the
// operator set the model themselves, it advises /model instead, once per
// pressed window. It returns what to tell the operator, "" for nothing.
func ReconcileDefaultModel(e DefaultModelEnv) string {
	cur, res := modellever.Current(e.Paths)
	if cur != nil && res.Status == modellever.StatusEdited {
		// The operator chose since: theirs now, and only the record goes.
		_, _ = modellever.Revert(e.Paths)
		cur = nil
	}
	hold := e.Rung == config.AutonomyAutonomous && e.Pressure != nil
	if cur != nil {
		if e.retracted(cur.From, cur.Value) {
			r, err := modellever.Revert(e.Paths)
			if err != nil || r.Status != modellever.StatusReverted {
				return ""
			}
			return quietOr(e.Verbosity, "tokenops: new Claude Code sessions start on your own default model again (starting them lower was retracted: quality dropped; `tokenops explain moves`).")
		}
		if hold || !defaultModelReasonPassed(e, cur) {
			return ""
		}
		r, err := modellever.Revert(e.Paths)
		if err != nil || r.Status != modellever.StatusReverted {
			return ""
		}
		return quietOr(e.Verbosity, fmt.Sprintf("tokenops: new Claude Code sessions start on your own default model again (%s).", revertWhy(e)))
	}
	if e.Pressure == nil || e.Rung == config.AutonomyOff {
		return ""
	}
	target, alias, ok := routeguard.SessionModelTarget(e.SessionModel, eventschema.ProviderAnthropic, e.Catalog, e.Candidates)
	if !ok {
		return ""
	}
	window := e.Pressure.Window + "|" + e.Pressure.ResetsAt.UTC().Format(time.RFC3339)
	if e.Rung == config.AutonomyAutonomous && !e.retracted(e.SessionModel, alias) {
		id := NewID()
		r, err := modellever.Apply(e.Paths, modellever.Setting{
			Value: alias, From: e.SessionModel, MoveID: id, Until: e.Pressure.ResetsAt,
			Reason:   fmt.Sprintf("%s; new sessions start on %s instead of %s", e.Pressure.Why(), target, e.SessionModel),
			Evidence: e.Pressure.Evidence(),
		}, e.Now)
		if err == nil && r.Status == modellever.StatusApplied {
			RecordMoveBecause(e.Ledger, e.Now, id, e.Session, config.PowerModels, DefaultModelKind, e.SessionModel, target, e.Pressure.Evidence())
			return quietOr(e.Verbosity, fmt.Sprintf(
				"tokenops: %s, so new Claude Code sessions will start on %s. This one keeps %s: /model %s switches it now. To stop: `tokenops coach set models advise`.",
				e.Pressure.Why(), alias, e.SessionModel, alias))
		}
		if err != nil || r.Status != modellever.StatusYours {
			return ""
		}
		// The operator set the model themselves: advise instead.
	}
	if !modellever.Advise(e.Paths, window) {
		return ""
	}
	id := NewID()
	RecordTip(e.Ledger, e.Now, id, e.Session, DefaultModelKind, ViaStopHook)
	return quietOr(e.Verbosity, fmt.Sprintf(
		"tokenops: %s. /model %s moves this session a tier down, and passing a cheaper model on subagent calls for work that fits stretches it further.",
		e.Pressure.Why(), alias))
}

// defaultModelReasonPassed reports whether the default tokenops set should
// go: the power is no longer autonomous, a fresh reading finds the plan no
// longer short, or the pressed window has reset. No reading at all is not
// a reason: the setting waits for one rather than flap.
func defaultModelReasonPassed(e DefaultModelEnv, cur *modellever.Setting) bool {
	switch {
	case e.Rung != config.AutonomyAutonomous:
		return true
	case e.Known && e.Pressure == nil:
		return true
	}
	return !cur.Until.IsZero() && e.Now.After(cur.Until)
}

func revertWhy(e DefaultModelEnv) string {
	if e.Rung != config.AutonomyAutonomous {
		return "models is no longer autonomous"
	}
	return "the Claude plan is no longer running short"
}

func quietOr(verbosity, msg string) string {
	if verbosity == config.VerbosityQuiet {
		return ""
	}
	return msg
}
