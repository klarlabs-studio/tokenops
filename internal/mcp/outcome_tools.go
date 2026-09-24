package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// OutcomeDeps wires outcome recording to the canonical local store.
type OutcomeDeps struct {
	Store *sqlite.Store
}

type outcomeInput struct {
	ExecutionID      string   `json:"execution_id" jsonschema:"description=The execution being assessed."`
	DecisionID       string   `json:"decision_id,omitempty" jsonschema:"description=The decision being evaluated, when known."`
	Result           string   `json:"result" jsonschema:"description=achieved | partial | not_achieved"`
	Caveat           string   `json:"caveat,omitempty" jsonschema:"description=Why the work was partial or unsuccessful, or useful context for the assessment."`
	AttentionMinutes *float64 `json:"attention_minutes,omitempty" jsonschema:"description=Optional active human effort in minutes, only when explicitly reported or confirmed by the operator."`
}

type outcomeResult struct {
	EventID     string `json:"event_id,omitempty"`
	ExecutionID string `json:"execution_id"`
	Result      string `json:"result"`
	Assessment  string `json:"assessment"`
	Recorded    bool   `json:"recorded"`
	Error       string `json:"error,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

type outcomeDetectInput struct {
	ExecutionID string `json:"execution_id"`
	DecisionID  string `json:"decision_id,omitempty"`
	SessionID   string `json:"session_id" jsonschema:"description=Claude Code session whose local transcript should be checked."`
}

// RegisterOutcomeTools adds explicit human outcome capture. It never infers a
// success from a completion marker.
func RegisterOutcomeTools(s *Server, d OutcomeDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_outcome_record").
		Description("Record the operator's assessment of whether one execution achieved its goal. Use only after the operator has actually judged the result; a task ending or an agent saying done is not an outcome. Optionally record active human attention minutes only when the operator explicitly reports or confirms the value. The assessment is stored locally and linked to the decision when decision_id is supplied.").
		OutputSchema(outcomeResult{}).
		Handler(func(ctx context.Context, in outcomeInput) (*outcomeResult, error) {
			if strings.TrimSpace(in.ExecutionID) == "" {
				return nil, fmt.Errorf("execution_id is required")
			}
			result, err := parseOutcomeResult(in.Result)
			if err != nil {
				return nil, err
			}
			if in.AttentionMinutes != nil && (math.IsNaN(*in.AttentionMinutes) || math.IsInf(*in.AttentionMinutes, 0) || *in.AttentionMinutes < 0) {
				return nil, fmt.Errorf("attention_minutes must be a finite non-negative number")
			}
			if d.Store == nil {
				return &outcomeResult{ExecutionID: in.ExecutionID, Result: string(result), Assessment: string(eventschema.OutcomeHuman), Error: "storage_disabled", Hint: "run `tokenops init` then restart the daemon"}, nil
			}
			env := outcomes.Event(outcomes.Record{
				ExecutionID: in.ExecutionID, DecisionID: in.DecisionID,
				Result: result, Assessment: eventschema.OutcomeHuman, Caveat: strings.TrimSpace(in.Caveat),
				AttentionMinutes: in.AttentionMinutes,
			})
			if err := correlateOutcome(ctx, d.Store, env); err != nil {
				return nil, err
			}
			if err := d.Store.Append(ctx, env); err != nil {
				return nil, err
			}
			return &outcomeResult{EventID: env.ID, ExecutionID: in.ExecutionID, Result: string(result), Assessment: string(eventschema.OutcomeHuman), Recorded: true}, nil
		})
	s.Tool("tokenops_outcome_detect").
		Description("Inspect a local Claude Code transcript for the final recognized verifier after the last edit and record that limited result as verification evidence. Returns unknown instead of treating completion as success when no verifier is present.").
		OutputSchema(outcomeResult{}).
		Handler(func(ctx context.Context, in outcomeDetectInput) (*outcomeResult, error) {
			if strings.TrimSpace(in.ExecutionID) == "" || strings.TrimSpace(in.SessionID) == "" {
				return nil, fmt.Errorf("execution_id and session_id are required")
			}
			if d.Store == nil {
				return &outcomeResult{ExecutionID: in.ExecutionID, Result: string(eventschema.OutcomeUnknown), Error: "storage_disabled", Hint: "run `tokenops init` then restart the daemon"}, nil
			}
			env, ok, err := outcomes.DetectSession(in.ExecutionID, in.DecisionID, in.SessionID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return &outcomeResult{ExecutionID: in.ExecutionID, Result: string(eventschema.OutcomeUnknown), Assessment: string(eventschema.OutcomeVerification), Error: "no_verifier", Hint: "run a recognized test or lint command after the final edit, or record a human outcome"}, nil
			}
			if err := correlateOutcome(ctx, d.Store, env); err != nil {
				return nil, err
			}
			if err := d.Store.Append(ctx, env); err != nil {
				return nil, err
			}
			out := env.Payload.(*eventschema.OutcomeEvent)
			return &outcomeResult{EventID: env.ID, ExecutionID: in.ExecutionID, Result: string(out.Result), Assessment: string(out.Assessment), Recorded: true}, nil
		})
	return nil
}

func correlateOutcome(ctx context.Context, store *sqlite.Store, env *eventschema.Envelope) error {
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

func parseOutcomeResult(raw string) (eventschema.OutcomeResult, error) {
	switch eventschema.OutcomeResult(strings.ToLower(strings.TrimSpace(raw))) {
	case eventschema.OutcomeAchieved:
		return eventschema.OutcomeAchieved, nil
	case eventschema.OutcomePartial:
		return eventschema.OutcomePartial, nil
	case eventschema.OutcomeNotAchieved:
		return eventschema.OutcomeNotAchieved, nil
	default:
		return eventschema.OutcomeUnknown, fmt.Errorf("result must be achieved, partial, or not_achieved")
	}
}
