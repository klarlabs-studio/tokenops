package mcp

import (
	"context"
	"errors"

	"go.klarlabs.de/tokenops/internal/capability/decisions"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// DecisionDeps wires explain-on-demand to the canonical event store, and
// the boundary tools to the config and the pause point's files.
type DecisionDeps struct {
	Store *sqlite.Store
	// ConfigPath is the config the boundaries are read from; empty takes
	// the default.
	ConfigPath string
	// Boundaries is where the pause point keeps its decisions; the zero
	// value is the default under ~/.tokenops.
	Boundaries bcap.Env
}

type boundaryDecideInput struct {
	ID       string `json:"id" jsonschema:"description=The boundary decision id (bnd-…) the agent was refused with, or one tokenops_boundaries (action=status) lists as pending"`
	Decision string `json:"decision" jsonschema:"enum=deny,description=deny refuses; approving is only possible from a terminal or the harness's prompt"`
	Session  bool   `json:"session,omitempty" jsonschema:"description=Answer for the rest of the session rather than for the next step only"`
}

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
	s.Tool("tokenops_boundaries_status").
		Description("Each action boundary in config.yaml: its stage (shadow or enforced), how often it matched or would have fired, and, per client, whether it is enforced or advice only; and the boundary decisions waiting on the operator.").
		OutputSchema(bcap.Status{}).
		Handler(func(_ context.Context, _ emptyInput) (*bcap.Status, error) {
			path := d.ConfigPath
			if path == "" {
				p, err := config.DefaultPath()
				if err != nil {
					return nil, err
				}
				path = p
			}
			cfg, err := config.Load(path)
			if err != nil {
				return nil, inputError(err)
			}
			st, err := bcap.RuleStatuses(cfg.BoundaryRules(), d.Boundaries, bcap.Wired(cfg))
			if err != nil {
				return nil, err
			}
			return &st, nil
		})
	s.Tool("tokenops_boundary_decide").
		Description("Refuse what a boundary asked about: deny refuses the agent's next step (or, with session, the rest of the session). Approving is not possible here: an approval must come from a person, with `tokenops approve <id>` in their own terminal or at Claude Code's permission prompt. The answer is recorded with the local user and method mcp, so explain shows who answered.").
		OutputSchema(bcap.Decided{}).
		Handler(func(_ context.Context, in boundaryDecideInput) (*bcap.Decided, error) {
			res, err := bcap.Decide(d.Boundaries, bcap.DecideRequest{ID: in.ID, Decision: in.Decision, Session: in.Session, Method: bcap.MethodMCP})
			var ie actions.InputError
			if errors.As(err, &ie) {
				return nil, inputError(err)
			}
			if err != nil {
				return nil, err
			}
			return &res, nil
		})
	return nil
}
