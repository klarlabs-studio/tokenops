package actions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// PreferredModels is every provider's preferred model after a change.
type PreferredModels struct {
	PreferredModels map[string]string `json:"preferred_models"`
	Config          string            `json:"config"`
}

// SetPreferredModel sets the model a provider's routing may not move
// above, or clears it. A route to a pricier model is then referred to the
// operator rather than applied.
func SetPreferredModel(path, provider, model string, clear bool) (PreferredModels, error) {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider == "" {
		return PreferredModels{}, inputErr(errors.New("provider is required"))
	}
	if !clear && model == "" {
		return PreferredModels{}, inputErr(errors.New("model is required unless clear=true"))
	}
	cfg, err := edit(path, func(c *config.Config) error {
		if clear {
			delete(c.PreferredModels, provider)
			return nil
		}
		if c.PreferredModels == nil {
			c.PreferredModels = map[string]string{}
		}
		c.PreferredModels[provider] = model
		return nil
	})
	if err != nil {
		return PreferredModels{}, err
	}
	return PreferredModels{PreferredModels: cfg.PreferredModels, Config: path}, nil
}

// RoutingDecision is the operator's answer to a routing proposal.
type RoutingDecision struct {
	Key      string `json:"key"`
	Decision string `json:"decision"`
	Model    string `json:"model"`
	Note     string `json:"note"`
}

// DecideRouting records the operator's answer to the proposal key in the
// approval store at storePath (empty: the default). approve routes future
// matching requests to the proposed model; deny keeps the requested one.
// It applies from the next request, with no restart.
func DecideRouting(storePath, key, decision string) (RoutingDecision, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return RoutingDecision{}, inputErr(errors.New("key is required (from the pending routing proposals)"))
	}
	if storePath == "" {
		p, err := routingapproval.DefaultPath()
		if err != nil {
			return RoutingDecision{}, err
		}
		storePath = p
	}
	store, err := routingapproval.Open(storePath)
	if err != nil {
		return RoutingDecision{}, err
	}
	state, err := store.Load()
	if err != nil {
		return RoutingDecision{}, err
	}
	st, ok := state[key]
	if !ok {
		return RoutingDecision{}, inputErr(errors.New("no routing proposal with key " + key))
	}
	var chosen string
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "approve", "approved":
		decision, chosen = "approved", st.To
	case "deny", "denied":
		decision, chosen = "denied", st.From
	default:
		return RoutingDecision{}, inputErr(errors.New(`decision must be "approve" or "deny"`))
	}
	if err := store.Decide(key, decision, chosen); err != nil {
		return RoutingDecision{}, err
	}
	return RoutingDecision{Key: key, Decision: decision, Model: chosen,
		Note: "applies to matching requests from now on; no restart needed"}, nil
}

// OutcomeRequest is the operator's judgement of one execution.
type OutcomeRequest struct {
	ExecutionID string
	DecisionID  string
	// Result is achieved, partial or not_achieved.
	Result string
	Caveat string
	// AttentionMinutes is active human effort, only when the operator
	// reported or confirmed it.
	AttentionMinutes *float64
}

// Outcome is a recorded judgement.
type Outcome struct {
	EventID     string `json:"event_id,omitempty"`
	ExecutionID string `json:"execution_id"`
	Result      string `json:"result"`
	Assessment  string `json:"assessment"`
	Recorded    bool   `json:"recorded"`
	Error       string `json:"error,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// RecordOutcome stores the operator's judgement of an execution, linked to
// its decision when one is named. A task ending or an agent saying done is
// not an outcome; only the operator's judgement is.
func RecordOutcome(ctx context.Context, store *sqlite.Store, req OutcomeRequest) (Outcome, error) {
	if strings.TrimSpace(req.ExecutionID) == "" {
		return Outcome{}, inputErr(errors.New("execution_id is required"))
	}
	result, err := ParseOutcomeResult(req.Result)
	if err != nil {
		return Outcome{}, inputErr(err)
	}
	if m := req.AttentionMinutes; m != nil && (math.IsNaN(*m) || math.IsInf(*m, 0) || *m < 0) {
		return Outcome{}, inputErr(errors.New("attention_minutes must be a finite non-negative number"))
	}
	out := Outcome{ExecutionID: req.ExecutionID, Result: string(result), Assessment: string(eventschema.OutcomeHuman)}
	if store == nil {
		out.Error, out.Hint = "storage_disabled", "run `tokenops init` then restart the daemon"
		return out, nil
	}
	env := outcomes.Event(outcomes.Record{
		ExecutionID: req.ExecutionID, DecisionID: req.DecisionID,
		Result: result, Assessment: eventschema.OutcomeHuman, Caveat: strings.TrimSpace(req.Caveat),
		AttentionMinutes: req.AttentionMinutes,
	})
	if err := CorrelateOutcome(ctx, store, env); err != nil {
		return Outcome{}, err
	}
	if err := store.Append(ctx, env); err != nil {
		return Outcome{}, err
	}
	out.EventID, out.Recorded = env.ID, true
	return out, nil
}

// CorrelateOutcome links an outcome to its decision's lifecycle.
func CorrelateOutcome(ctx context.Context, store *sqlite.Store, env *eventschema.Envelope) error {
	if env.Correlation.Decision == "" {
		return nil
	}
	history, err := store.Query(ctx, sqlite.Filter{Decision: env.Correlation.Decision, Limit: 10_000})
	if err != nil {
		return err
	}
	outcomes.CorrelateDecisionLifecycle(env, history)
	return nil
}

// ParseOutcomeResult reads achieved, partial or not_achieved.
func ParseOutcomeResult(raw string) (eventschema.OutcomeResult, error) {
	switch r := eventschema.OutcomeResult(strings.ToLower(strings.TrimSpace(raw))); r {
	case eventschema.OutcomeAchieved, eventschema.OutcomePartial, eventschema.OutcomeNotAchieved:
		return r, nil
	default:
		return eventschema.OutcomeUnknown, fmt.Errorf("result must be achieved, partial, or not_achieved")
	}
}
