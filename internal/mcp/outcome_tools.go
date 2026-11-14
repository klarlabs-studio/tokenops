package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	mcpserver "go.klarlabs.de/mcp/server"

	"go.klarlabs.de/tokenops/internal/capability/actions"

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

// outcomeResult is the actions capability's payload, shared with the
// daemon API (ADR 0010 §4).
type outcomeResult = actions.Outcome

type outcomeDetectInput struct {
	ExecutionID string `json:"execution_id"`
	DecisionID  string `json:"decision_id,omitempty"`
	SessionID   string `json:"session_id" jsonschema:"description=Claude Code session whose local transcript should be checked."`
}

// An agent that reports how its own work went is grading itself, and
// agents reliably report success. So a report over MCP is recorded as the
// operator's judgement only when the operator confirms it at the client,
// through elicitation and outside the agent's turn; otherwise it is kept as
// the agent's self-report, which learning does not count as strong evidence.

// outcomeConfirmSchema is the form the operator answers: their result, a
// box to tick, and their attention minutes if they want them kept.
func outcomeConfirmSchema(req actions.OutcomeRequest) map[string]any {
	minutes := map[string]any{
		"type": "number", "minimum": 0,
		"title":       "Your active minutes (optional)",
		"description": "Minutes of your own attention the work took, kept only if you tick the box.",
	}
	if req.AttentionMinutes != nil {
		minutes["default"] = *req.AttentionMinutes
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": map[string]any{
				"type": "string", "title": "How did the work turn out?",
				"enum":    []string{string(eventschema.OutcomeAchieved), string(eventschema.OutcomePartial), string(eventschema.OutcomeNotAchieved)},
				"default": strings.ToLower(strings.TrimSpace(req.Result)),
			},
			"confirm": map[string]any{
				"type": "boolean", "title": "This is my judgement",
				"description": "Tick only if you looked at the work yourself.",
				"default":     false,
			},
			"attention_minutes": minutes,
		},
		"required": []string{"result", "confirm"},
	}
}

// askOperatorAboutOutcome asks the operator to confirm or correct an
// agent's report. It returns the request to record — the operator's, or the
// agent's own when they did not confirm — whether they declined it, and a
// note for the agent on what was recorded.
func askOperatorAboutOutcome(ctx context.Context, req actions.OutcomeRequest) (actions.OutcomeRequest, bool, string, error) {
	const unconfirmed = "kept as your own report (self_report), which TokenOps does not learn from: "
	el := mcpserver.ElicitFromContext(ctx)
	if el == nil {
		return req, false, unconfirmed + "this client cannot ask the operator to confirm it. The operator records their judgement with: " + outcomeCommand(req), nil
	}
	summary := fmt.Sprintf("execution %s: %s", quoted(req.ExecutionID), quoted(req.Result))
	if c := strings.TrimSpace(req.Caveat); c != "" {
		summary += "; caveat " + quoted(c)
	}
	res, err := el.Elicit(ctx, &mcpserver.ElicitRequest{
		Message: "An agent reports how a piece of work turned out:\n\n" + summary +
			"\n\nConfirm it, or correct the result, only if you judged the work yourself. Unconfirmed, it is kept as the agent's own report, which TokenOps does not learn from.",
		RequestedSchema: outcomeConfirmSchema(req),
	})
	if errors.Is(err, mcpserver.ErrInputRequired) {
		return req, false, "", err // a stateless client retries with the answer
	}
	if err != nil {
		return req, false, unconfirmed + "the operator could not be asked. They record their judgement with: " + outcomeCommand(req), nil
	}
	switch res.Action {
	case "accept":
		if ok, _ := res.Content["confirm"].(bool); !ok {
			return req, false, unconfirmed + "the operator did not tick the box.", nil
		}
		result, _ := res.Content["result"].(string)
		if _, err := actions.ParseOutcomeResult(result); err != nil {
			return req, false, unconfirmed + "the operator's answer named no result.", nil
		}
		confirmed := req
		confirmed.Result, confirmed.Via, confirmed.AttentionMinutes = result, outcomes.ViaOperatorConfirmed, nil
		if m, ok := res.Content["attention_minutes"].(float64); ok {
			confirmed.AttentionMinutes = &m
		}
		if _, err := actions.ValidateOutcome(confirmed); err != nil {
			confirmed.AttentionMinutes = nil
		}
		return confirmed, false, "", nil
	case "decline":
		return req, true, "", nil
	default:
		return req, false, unconfirmed + "the operator dismissed the confirmation.", nil
	}
}

// outcomeCommand is the terminal command that records req as the
// operator's judgement.
func outcomeCommand(req actions.OutcomeRequest) string {
	cmd := "tokenops outcome record " + shellArg(req.ExecutionID) + " --result " + shellArg(strings.ToLower(strings.TrimSpace(req.Result)))
	if req.DecisionID != "" {
		cmd += " --decision-id " + shellArg(req.DecisionID)
	}
	return cmd
}

// RegisterOutcomeTools adds outcome reports, recorded as the operator's
// judgement only when they confirm them, and verifier detection. It never
// infers a success from a completion marker.
func RegisterOutcomeTools(s *Server, d OutcomeDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_outcome_record").
		Description("Report whether one execution achieved its goal. TokenOps asks the operator at the client to confirm or correct the report; only an outcome the operator confirms is recorded as their judgement (assessment human) and counts as strong evidence for learning. Without their confirmation — a client that cannot ask, a dismissed or unticked dialog — it is kept as the agent's own report (assessment self_report, recorded_via mcp_agent), which learning does not count; if the operator declines, nothing is recorded. Report what happened, not what you hope: a task ending or an agent saying done is not an outcome. Attention minutes are kept only when the operator confirms them. The outcome is stored locally and linked to the decision when decision_id is supplied.").
		OutputSchema(outcomeResult{}).
		Handler(func(ctx context.Context, in outcomeInput) (*outcomeResult, error) {
			req := actions.OutcomeRequest{
				ExecutionID: in.ExecutionID, DecisionID: in.DecisionID, Result: in.Result,
				Caveat: in.Caveat, AttentionMinutes: in.AttentionMinutes, Via: outcomes.ViaAgent,
			}
			if _, err := actions.ValidateOutcome(req); err != nil {
				return nil, actionError(err)
			}
			note := ""
			if d.Store != nil {
				var (
					declined bool
					err      error
				)
				req, declined, note, err = askOperatorAboutOutcome(ctx, req)
				if err != nil {
					return nil, err
				}
				if declined {
					return &outcomeResult{
						ExecutionID: in.ExecutionID, Result: req.Result, Error: "declined",
						Hint: "the operator declined to confirm this report, so nothing was recorded; do not retry unless the operator asks",
					}, nil
				}
			}
			res, err := actions.RecordOutcome(ctx, d.Store, req)
			if err != nil {
				return nil, actionError(err)
			}
			if res.Recorded && note != "" {
				res.Hint = note
			}
			return &res, nil
		})
	s.Tool("tokenops_outcome_detect").
		Description("Inspect a local Claude Code transcript for the final recognized verifier after the last edit and record that limited result as verification evidence. Returns unknown instead of treating completion as success when no verifier is present.").
		OutputSchema(outcomeResult{}).
		Handler(func(ctx context.Context, in outcomeDetectInput) (*outcomeResult, error) {
			if strings.TrimSpace(in.ExecutionID) == "" || strings.TrimSpace(in.SessionID) == "" {
				return nil, inputError(errors.New("execution_id and session_id are required"))
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
			if err := actions.CorrelateOutcome(ctx, d.Store, env); err != nil {
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
