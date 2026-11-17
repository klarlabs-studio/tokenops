package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	mcpgo "go.klarlabs.de/mcp"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/workestimate"
	"go.klarlabs.de/tokenops/internal/capability/workflowtrace"
	"go.klarlabs.de/tokenops/internal/capability/workquote"
	"go.klarlabs.de/tokenops/internal/presentation"
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
	WorkflowID     string                       `json:"workflow_id"`
	EvidenceStatus string                       `json:"evidence_status"`
	Insight        presentation.WorkInsight     `json:"insight"`
	Trace          reviewWorkTrace              `json:"trace"`
	Usage          *spendSummaryResult          `json:"usage"`
	Findings       []*eventschema.CoachingEvent `json:"findings"`
	// WindowShare sets the share of the plan window the task took against
	// the estimate made at prepare. Absent for a workflow that was never
	// prepared, since there is no starting point to measure from.
	WindowShare *workestimate.ReviewResult `json:"window_share,omitempty"`
	// Quote sets the quote prepare gave against the measured cost and
	// window share, with how earlier quotes fared. Absent for a workflow
	// that was never quoted.
	Quote *workquote.Check `json:"quote,omitempty"`
}

func registerIntentTools(s *Server, d Deps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_review_work").
		Description("Review one completed or in-progress workflow in one call. Pass the workflow_id returned by tokenops_prepare_work. The result explicitly distinguishes measured evidence from no_evidence, summarizes steps and context growth, measures token and cost totals, sets the quote prepare gave against what the task took (with how often quotes stayed within their p90), and returns existing coaching findings. Findings are evidence-based suggestions, not applied changes. Returns aggregate trace metrics, not prompt content; no finding is not a quality assessment.").
		OutputSchema(reviewWorkResult{}).
		Handler(func(ctx context.Context, in reviewWorkInput) (mcpgo.StructuredResult, error) {
			trace, err := workflowTrace(ctx, d, workflowTraceInput(in))
			noEvidence := errors.Is(err, workflowtrace.ErrNoTrace)
			if err != nil && !noEvidence {
				return mcpgo.StructuredResult{}, err
			}
			usage, err := spendSummary(ctx, d, spendSummaryInput{WorkflowID: in.WorkflowID})
			if err != nil {
				return mcpgo.StructuredResult{}, err
			}
			findings := []*eventschema.CoachingEvent{}
			if !noEvidence && trace.Findings != nil {
				findings = trace.Findings
			}
			result := &reviewWorkResult{
				WorkflowID:     in.WorkflowID,
				EvidenceStatus: "no_evidence",
				Insight:        workInsight(0, findings),
				Trace:          reviewWorkTrace{Models: map[string]int{}},
				Usage:          usage,
				Findings:       findings,
			}
			if !noEvidence {
				result.EvidenceStatus = "measured"
				result.Insight = workInsight(trace.Trace.StepCount, findings)
				result.Trace = reviewWorkTrace{
					StepCount:         trace.Trace.StepCount,
					TotalInputTokens:  trace.Trace.TotalInputTokens,
					TotalOutputTokens: trace.Trace.TotalOutputTokens,
					TotalTokens:       trace.Trace.TotalTotalTokens,
					TotalCostUSD:      trace.Trace.TotalCostUSD,
					MaxContextTokens:  trace.Trace.MaxContextSize,
					ContextGrowth:     trace.Trace.ContextGrowthTotal,
					Models:            trace.Trace.Models,
				}
				if d.CoachLedger != nil {
					// The models the work ran on settle the model tip
					// prepare gave for it.
					coachcap.SettleWorkflowTips(d.CoachLedger(), time.Now(), in.WorkflowID, trace.Trace.Models)
				}
			}
			if d.Store != nil {
				share, err := workestimate.Review(ctx, d.Store, in.WorkflowID, time.Now().UTC())
				if err != nil {
					return mcpgo.StructuredResult{}, err
				}
				result.WindowShare = share
				check, err := workquote.Review(ctx, d.Store, in.WorkflowID, quoteActual(result), time.Now().UTC())
				if err != nil {
					return mcpgo.StructuredResult{}, err
				}
				result.Quote = check
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return mcpgo.StructuredResult{}, err
			}
			var structured map[string]any
			if err := json.Unmarshal(encoded, &structured); err != nil {
				return mcpgo.StructuredResult{}, err
			}
			return mcpgo.StructuredResult{
				Content:           []mcpgo.Content{mcpgo.NewTextContent(markdownPayload(renderWorkInsight(result), result))},
				StructuredContent: structured,
			}, nil
		})
	return nil
}

// quoteActual is what review measured of the task, for checking its
// quote: the cost of the turns that carried the workflow, and the share of
// the plan window the meter read across it.
func quoteActual(r *reviewWorkResult) workquote.Actual {
	var a workquote.Actual
	if r.EvidenceStatus == "measured" && r.Trace.TotalCostUSD > 0 {
		a.CostUSD, a.CostMeasured = r.Trace.TotalCostUSD, true
	}
	if ws := r.WindowShare; ws != nil && ws.Actual.Measured {
		if d, ok := windowLength(ws.Window); ok {
			a.Window, a.SharePct, a.ShareMeasured = d, ws.Actual.SharePct, true
		}
	}
	return a
}

// windowLength reads a window label as workestimate writes it: 5h, 7d.
func windowLength(label string) (time.Duration, bool) {
	if len(label) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(label[:len(label)-1])
	if err != nil || n <= 0 {
		return 0, false
	}
	switch label[len(label)-1] {
	case 'h':
		return time.Duration(n) * time.Hour, true
	case 'd':
		return time.Duration(n) * 24 * time.Hour, true
	}
	return 0, false
}

func workInsight(stepCount int, findings []*eventschema.CoachingEvent) presentation.WorkInsight {
	summaries := make([]string, 0, len(findings))
	for _, finding := range findings {
		if finding != nil {
			summaries = append(summaries, finding.Summary)
		}
	}
	return presentation.ForWork(stepCount, summaries)
}

func renderWorkInsight(result *reviewWorkResult) string {
	body := renderWorkEvidence(result)
	if result.WindowShare != nil {
		body += "\n\nPlan window: " + result.WindowShare.Summary + "."
	}
	if result.Quote != nil {
		body += "\n\nQuote: " + result.Quote.Summary + "."
	}
	return body
}

func renderWorkEvidence(result *reviewWorkResult) string {
	if result.EvidenceStatus == "no_evidence" {
		return fmt.Sprintf("## Work insight — `%s`\n\n%s\n\nEvidence: no_evidence. No matching execution measurements were observed for `%s`; zero-valued fields are placeholders, not measured zero usage. No changes were applied.",
			result.Insight.Level, result.Insight.Summary, result.WorkflowID)
	}
	return fmt.Sprintf("## Work insight — `%s`\n\n%s\n\nEvidence: %s. Measured: %d steps · %d tokens · $%.4f · %d tokens context growth. Findings are suggestions only; no changes were applied.",
		result.Insight.Level, result.Insight.Summary, result.EvidenceStatus, result.Trace.StepCount,
		result.Trace.TotalTokens, result.Trace.TotalCostUSD, result.Trace.ContextGrowth)
}
