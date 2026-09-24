package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/explain"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// DecisionDeps wires explain-on-demand to the canonical event store.
type DecisionDeps struct{ Store *sqlite.Store }

type explainDecisionInput struct {
	DecisionID string `json:"decision_id" jsonschema:"description=Decision identifier returned by a TokenOps recommendation."`
}

type explainDecisionResult struct {
	Report *explain.Report `json:"report,omitempty"`
	Error  string          `json:"error,omitempty"`
	Hint   string          `json:"hint,omitempty"`
}

// RegisterDecisionTools exposes explanations captured at decision time.
func RegisterDecisionTools(s *Server, d DecisionDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_explain_decision").
		Description("Explain why TokenOps made a decision from the evidence, alternatives, policy, authority and uncertainty captured at decision time, then include the strongest later outcome assessment. Requires the decision_id returned by the original recommendation.").
		OutputSchema(explainDecisionResult{}).
		Handler(func(ctx context.Context, in explainDecisionInput) (*explainDecisionResult, error) {
			id := strings.TrimSpace(in.DecisionID)
			if id == "" {
				return nil, fmt.Errorf("decision_id is required")
			}
			if d.Store == nil {
				return &explainDecisionResult{Error: "storage_disabled", Hint: "run `tokenops init` then restart the daemon"}, nil
			}
			events, err := d.Store.Query(ctx, sqlite.Filter{Decision: id, Limit: 10_000})
			if err != nil {
				return nil, err
			}
			report, ok := explain.Build(id, events)
			if !ok {
				return &explainDecisionResult{Error: "decision_not_found", Hint: "use the decision_id returned by tokenops_routing_advise or a proxy intervention"}, nil
			}
			return &explainDecisionResult{Report: &report}, nil
		})
	return nil
}
