package mcp

import (
	"context"
	"errors"
	"strings"
)

// prepareWorkResult composes resource headroom and per-turn routing advice.
type prepareWorkResult struct {
	Recommendation routingAdviceResult `json:"recommendation"`
	PlanHeadroom   planHeadroomResult  `json:"plan_headroom"`
}

func registerWorkPreparationTool(s *Server, d RoutingAdviceDeps) error {
	planDeps := PlanDeps{Config: d.Config, ConfigGetter: d.ConfigGetter, Store: d.Store}
	s.Tool("tokenops_prepare_work").
		Description("Before starting a task, get one evidence-backed view of subscription headroom and the model recommendation for the supplied instruction. The result combines plan headroom with TokenOps routing policy, includes measurement caveats, and never changes the caller's model. Pass provider when several plans are configured; pass the current model to compare alternatives.").
		OutputSchema(prepareWorkResult{}).
		Handler(func(ctx context.Context, in routingAdviceInput) (*prepareWorkResult, error) {
			if strings.TrimSpace(in.Instruction) == "" {
				return nil, inputError(errors.New("instruction is required"))
			}
			if strings.TrimSpace(in.Model) == "" {
				return nil, inputError(errors.New("model is required to compare routing alternatives"))
			}
			headroom, err := planHeadroom(ctx, planDeps)
			if err != nil {
				return nil, err
			}
			recommendation, err := routingAdvice(ctx, in, d)
			if err != nil {
				return nil, err
			}
			return &prepareWorkResult{Recommendation: *recommendation, PlanHeadroom: *headroom}, nil
		})
	return nil
}
