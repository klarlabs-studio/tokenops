package mcp

import (
	"context"
	"errors"

	"go.klarlabs.de/tokenops/internal/capability/decisions"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// DecisionDeps wires explain-on-demand to the canonical event store.
type DecisionDeps struct{ Store *sqlite.Store }

type explainDecisionInput struct {
	DecisionID string `json:"decision_id" jsonschema:"description=Decision identifier returned by a TokenOps recommendation."`
}

// explainDecisionResult is the decisions capability's payload, shared with
// the daemon API (ADR 0010 §4).
type explainDecisionResult = decisions.Explanation

// RegisterDecisionTools exposes explanations captured at decision time.
func RegisterDecisionTools(s *Server, d DecisionDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_explain_decision").
		Description("Explain why TokenOps made a decision from the evidence, alternatives, policy, authority and uncertainty captured at decision time, then include the strongest later outcome assessment. Requires the decision_id returned by the original recommendation.").
		OutputSchema(explainDecisionResult{}).
		Handler(func(ctx context.Context, in explainDecisionInput) (*explainDecisionResult, error) {
			res, err := decisions.Explain(ctx, d.Store, in.DecisionID)
			if errors.Is(err, decisions.ErrMissingID) {
				return nil, inputError(err)
			}
			if err != nil {
				return nil, err
			}
			return &res, nil
		})
	return nil
}
