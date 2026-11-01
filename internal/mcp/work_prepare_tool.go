package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/workestimate"
	"go.klarlabs.de/tokenops/internal/config"
)

const workflowIDHeader = "X-Tokenops-Workflow-Id"

// workReviewHandoff makes the before/after contract explicit without claiming
// that TokenOps runs the work. The caller propagates WorkflowID while it
// executes, then gives the same identifier back to the review tool.
type workReviewHandoff struct {
	Tool       string `json:"tool"`
	WorkflowID string `json:"workflow_id"`
}

// prepareWorkInput is the routing-advice input plus where the work runs,
// which scopes the window-share history.
type prepareWorkInput struct {
	Instruction string  `json:"instruction" jsonschema:"required,description=The operator's instruction you are about to act on, or the task you are about to hand a subagent. Pass it verbatim; it is classified locally and never stored."`
	Provider    string  `json:"provider,omitempty" jsonschema:"description=Provider name, e.g. anthropic. Defaults to the single configured plan when there is only one."`
	Model       string  `json:"model,omitempty" jsonschema:"description=The model this turn would otherwise run on. Without it there is nothing to compare against."`
	ToolDensity float64 `json:"tool_density,omitempty" jsonschema:"description=Share of the recent exchange that was tool traffic (0-1), when you can estimate it. Omitting it only makes the answer more conservative."`
	Repository  string  `json:"repository,omitempty" jsonschema:"description=The repository or project the task runs in, e.g. its directory name. The window-share estimate is drawn from earlier tasks of the same kind there, falling back to the same kind anywhere."`
	WorkID      string  `json:"work_id,omitempty" jsonschema:"description=Stable work identifier when the caller already knows it."`
	ExecutionID string  `json:"execution_id,omitempty" jsonschema:"description=Stable execution identifier when the caller already knows it."`
	ActorID     string  `json:"actor_id,omitempty" jsonschema:"description=Actor performing the execution when known."`
	WorkflowID  string  `json:"workflow_id,omitempty" jsonschema:"description=Stable workflow identifier to preserve across prepare, execution attribution, and review. tokenops_prepare_work generates one when omitted."`
}

func (in prepareWorkInput) advice() routingAdviceInput {
	return routingAdviceInput{
		Instruction: in.Instruction, Provider: in.Provider, Model: in.Model, ToolDensity: in.ToolDensity,
		WorkID: in.WorkID, ExecutionID: in.ExecutionID, ActorID: in.ActorID, WorkflowID: in.WorkflowID,
	}
}

// windowShareEstimate is the capability's payload: the task's expected
// share of the plan window, from history.
type windowShareEstimate = workestimate.Preparation

// prepareWorkResult composes resource headroom, per-turn routing advice and
// the task's expected share of the plan window.
type prepareWorkResult struct {
	WorkflowID        string               `json:"workflow_id"`
	AttributionHeader string               `json:"attribution_header"`
	Review            workReviewHandoff    `json:"review"`
	Recommendation    routingAdviceResult  `json:"recommendation"`
	PlanHeadroom      planHeadroomResult   `json:"plan_headroom"`
	WindowShare       *windowShareEstimate `json:"window_share,omitempty"`
	// CoachTips are the coach's tips for the work about to start, each
	// naming one object (a window, a model): given here, at the decision,
	// rather than after the turn. The coach's dials, its throttle and
	// the kinds it retired decide which are given; empty when none is.
	CoachTips []coachcap.DecisionTip `json:"coach_tips,omitempty"`
}

func registerWorkPreparationTool(s *Server, d RoutingAdviceDeps) error {
	planDeps := PlanDeps{Config: d.Config, ConfigGetter: d.ConfigGetter, Store: d.Store, Spend: d.Spend}
	s.Tool("tokenops_prepare_work").
		Description("Before starting a task, get one evidence-backed view of subscription headroom, the model recommendation for the supplied instruction, and the task's expected share of the plan window (a range from earlier tasks of the same kind in the same repository, suppressed until there are enough of them, plus the p50 and p90 of those runs with their count, since runs of one task vary widely; read only from measured outcomes, never from an agent's own guess; with a suggestion to proceed, use a lighter model tier, or defer). The result includes a stable workflow_id: propagate it as X-Tokenops-Workflow-Id when the execution path supports request headers, then pass it unchanged to tokenops_review_work, which records the actual share against the estimate. TokenOps issues correlation here but does not claim that execution started. The tool includes measurement caveats and never changes the caller's model. It also returns coach_tips: the coach's tips for the work about to start, each naming one window or model, given at most a few an hour and never the same twice in a row. Pass provider when several plans are configured; pass the current model to compare alternatives; pass repository to scope the estimate.").
		OutputSchema(prepareWorkResult{}).
		Handler(func(ctx context.Context, in prepareWorkInput) (*prepareWorkResult, error) {
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
			recommendation, err := routingAdvice(ctx, in.advice(), d)
			if err != nil {
				return nil, err
			}
			share, err := windowShare(ctx, d, in, workflowID, recommendation)
			if err != nil {
				return nil, err
			}
			tips := prepareTips(d, in, workflowID, recommendation, headroom, share)
			return &prepareWorkResult{
				WorkflowID:        workflowID,
				AttributionHeader: workflowIDHeader,
				Review: workReviewHandoff{
					Tool:       "tokenops_review_work",
					WorkflowID: workflowID,
				},
				Recommendation: *recommendation,
				PlanHeadroom:   *headroom,
				WindowShare:    share,
				CoachTips:      tips,
			}, nil
		})
	return nil
}

// windowShare estimates the task's share of the plan window and records
// the window's state, so review can measure what the task took.
func windowShare(ctx context.Context, d RoutingAdviceDeps, in prepareWorkInput, workflowID string, rec *routingAdviceResult) (*windowShareEstimate, error) {
	deps := workestimate.Deps{Config: d.activeConfig()}
	if d.Store != nil {
		deps.Store = d.Store
	}
	req := workestimate.PrepareRequest{
		WorkflowID: workflowID, Instruction: in.Instruction, Scope: in.Repository,
		Provider: in.Provider, Model: in.Model,
	}
	if rec != nil && rec.Recommendation == "switch" {
		req.SwitchTo = rec.Model
	}
	return workestimate.Prepare(ctx, deps, req, time.Now().UTC())
}

// prepareTips are the coach's tips for the work about to start, given
// through the decision-point gate and recorded against the workflow, so
// tokenops_review_work can settle the model tip from what the work ran
// on. An MCP call carries no session, so these tips share one hourly
// budget.
func prepareTips(d RoutingAdviceDeps, in prepareWorkInput, workflowID string, rec *routingAdviceResult, headroom *planHeadroomResult, share *windowShareEstimate) []coachcap.DecisionTip {
	tin := coachcap.PrepareTipInput{CurrentModel: strings.TrimSpace(in.Model)}
	if rec != nil {
		tin.Recommendation, tin.Model, tin.Reason = rec.Recommendation, rec.Model, rec.Reason
	}
	if share != nil && share.Provider != "" && share.Window != "" {
		tin.ShareAction, tin.ShareReason = string(share.Suggestion.Action), share.Suggestion.Reason
		tin.ShareWindow = share.Provider + " " + share.Window + " window"
	}
	if headroom != nil {
		for _, r := range headroom.Reports {
			for _, w := range r.Windows {
				if w.Stale || w.NotStarted {
					continue
				}
				pw := coachcap.PrepareWindow{Name: r.Provider + " " + w.Name + " window", UsedPct: w.UsedPct, ResetsIn: w.ResetsIn}
				if w.Pace != nil && !w.Pace.LastsToReset {
					pw.RunsOutIn = w.Pace.RunsOutIn
				}
				tin.Windows = append(tin.Windows, pw)
			}
		}
	}
	candidates := coachcap.PrepareWorkTips(tin)
	if len(candidates) == 0 {
		return nil
	}
	cfg := config.Default()
	if c := d.activeConfig(); c != nil {
		cfg = *c
	}
	var ledger coachcap.Ledger
	if d.CoachLedger != nil {
		ledger = d.CoachLedger()
	}
	return coachcap.NewTipGate(cfg, ledger, time.Now()).Deliver("", workflowID, candidates)
}
