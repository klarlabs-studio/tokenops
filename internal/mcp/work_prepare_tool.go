package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const workflowIDHeader = "X-Tokenops-Workflow-Id"

// workReviewHandoff makes the before/after contract explicit without claiming
// that TokenOps runs the work. The caller propagates WorkflowID while it
// executes, then gives the same identifier back to the review tool.
type workReviewHandoff struct {
	Tool       string `json:"tool"`
	WorkflowID string `json:"workflow_id"`
}

// prepareWorkResult composes resource headroom and per-turn routing advice.
type prepareWorkResult struct {
	WorkflowID        string              `json:"workflow_id"`
	AttributionHeader string              `json:"attribution_header"`
	Review            workReviewHandoff   `json:"review"`
	Recommendation    routingAdviceResult `json:"recommendation"`
	PlanHeadroom      planHeadroomResult  `json:"plan_headroom"`
}

func registerWorkPreparationTool(s *Server, d RoutingAdviceDeps) error {
	planDeps := PlanDeps{Config: d.Config, ConfigGetter: d.ConfigGetter, Store: d.Store}
	s.Tool("tokenops_prepare_work").
		Description("Before starting a task, get one evidence-backed view of subscription headroom and the model recommendation for the supplied instruction. The result includes a stable workflow_id: propagate it as X-Tokenops-Workflow-Id when the execution path supports request headers, then pass it unchanged to tokenops_review_work. TokenOps issues correlation here but does not claim that execution started. The tool includes measurement caveats and never changes the caller's model. Pass provider when several plans are configured; pass the current model to compare alternatives.").
		OutputSchema(prepareWorkResult{}).
		Handler(func(ctx context.Context, in routingAdviceInput) (*prepareWorkResult, error) {
			if strings.TrimSpace(in.Instruction) == "" {
				return nil, inputError(errors.New("instruction is required"))
			}
			if strings.TrimSpace(in.Model) == "" {
				return nil, inputError(errors.New("model is required to compare routing alternatives"))
			}
			workflowID := strings.TrimSpace(in.WorkflowID)
			if workflowID == "" {
				workflowID = "workflow:" + uuid.NewString()
			}
			headroom, err := planHeadroom(ctx, planDeps)
			if err != nil {
				return nil, err
			}
			recommendation, err := routingAdvice(ctx, in, d)
			if err != nil {
				return nil, err
			}
			return &prepareWorkResult{
				WorkflowID:        workflowID,
				AttributionHeader: workflowIDHeader,
				Review: workReviewHandoff{
					Tool:       "tokenops_review_work",
					WorkflowID: workflowID,
				},
				Recommendation: *recommendation,
				PlanHeadroom:   *headroom,
			}, nil
		})
	return nil
}
