package mcp

import (
	"context"
	"errors"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type reviewWorkInput struct {
	WorkflowID string `json:"workflow_id" jsonschema:"required,description=Stable workflow identifier from the execution being reviewed"`
}

// reviewWorkTrace is the compact task-level measurement that helps an agent
// spot context growth without returning every prompt event in the trace.
type reviewWorkTrace struct {
	StepCount         int            `json:"step_count"`
	TotalInputTokens  int64          `json:"total_input_tokens"`
	TotalOutputTokens int64          `json:"total_output_tokens"`
	TotalTokens       int64          `json:"total_tokens"`
	TotalCostUSD      float64        `json:"total_cost_usd"`
	MaxContextTokens  int64          `json:"max_context_tokens"`
	ContextGrowth     int64          `json:"context_growth_tokens"`
	Models            map[string]int `json:"models"`
}

// reviewWorkResult composes the measured work trace, spend view, and existing
// waste-detector findings into one task-oriented response.
type reviewWorkResult struct {
	WorkflowID string                       `json:"workflow_id"`
	Trace      reviewWorkTrace              `json:"trace"`
	Usage      *spendSummaryResult          `json:"usage"`
	Findings   []*eventschema.CoachingEvent `json:"findings"`
}

func registerIntentTools(s *Server, d Deps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_review_work").
		Description("Review one completed or in-progress workflow in one call: summarize its steps and context growth, measure token and cost totals, and return existing coaching findings. Use this when asked why a task consumed resources or how to reduce waste on a specific workflow. Findings are evidence-based suggestions, not applied changes. Returns aggregate trace metrics, not prompt content.").
		OutputSchema(reviewWorkResult{}).
		Handler(func(ctx context.Context, in reviewWorkInput) (*reviewWorkResult, error) {
			trace, err := workflowTrace(ctx, d, workflowTraceInput(in))
			if err != nil {
				return nil, err
			}
			usage, err := spendSummary(ctx, d, spendSummaryInput{WorkflowID: in.WorkflowID})
			if err != nil {
				return nil, err
			}
			findings := trace.Findings
			if findings == nil {
				findings = []*eventschema.CoachingEvent{}
			}
			return &reviewWorkResult{
				WorkflowID: in.WorkflowID,
				Trace: reviewWorkTrace{
					StepCount:         trace.Trace.StepCount,
					TotalInputTokens:  trace.Trace.TotalInputTokens,
					TotalOutputTokens: trace.Trace.TotalOutputTokens,
					TotalTokens:       trace.Trace.TotalTotalTokens,
					TotalCostUSD:      trace.Trace.TotalCostUSD,
					MaxContextTokens:  trace.Trace.MaxContextSize,
					ContextGrowth:     trace.Trace.ContextGrowthTotal,
					Models:            trace.Trace.Models,
				},
				Usage:    usage,
				Findings: findings,
			}, nil
		})
	return nil
}
