package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpgo "go.klarlabs.de/mcp"

	"go.klarlabs.de/tokenops/internal/presentation"
)

// resourceGlanceResult composes current rate-limit and plan headroom signals.
// The insight is a compact orientation; the original measurements and their
// caveats remain available for inspection.
type resourceGlanceResult struct {
	Insight       presentation.ResourceInsight `json:"insight"`
	SessionBudget sessionBudgetResult          `json:"session_budget"`
	PlanHeadroom  *planHeadroomResult          `json:"plan_headroom"`
}

func registerResourceGlanceTool(s *Server, d PlanDeps) {
	s.Tool("tokenops_resource_glance").
		Description("Give one compact view of current AI-resource pressure by composing the existing session budget and plan headroom measurements. The insight never changes a model or applies an action; it carries signal quality and caveats, and says uncertain or unavailable when evidence does not support a clear reading.").
		OutputSchema(resourceGlanceResult{}).
		Handler(func(ctx context.Context, _ emptyInput) (mcpgo.StructuredResult, error) {
			session, err := sessionBudgetData(ctx, d)
			if err != nil {
				return mcpgo.StructuredResult{}, err
			}
			headroom, err := planHeadroom(ctx, d)
			if err != nil {
				return mcpgo.StructuredResult{}, err
			}
			result := &resourceGlanceResult{
				SessionBudget: *session,
				PlanHeadroom:  headroom,
			}
			result.Insight = resourceInsight(session, headroom)
			encoded, err := json.Marshal(result)
			if err != nil {
				return mcpgo.StructuredResult{}, err
			}
			var structured map[string]any
			if err := json.Unmarshal(encoded, &structured); err != nil {
				return mcpgo.StructuredResult{}, err
			}
			return mcpgo.StructuredResult{
				Content:           []mcpgo.Content{mcpgo.NewTextContent(markdownPayload(renderResourceGlance(result.Insight), result))},
				StructuredContent: structured,
			}, nil
		})
}

func renderResourceGlance(insight presentation.ResourceInsight) string {
	heading := fmt.Sprintf("## Resource glance — `%s`", insight.Level)
	if insight.Provider != "" {
		heading += " · " + insight.Provider
	}
	summary := heading + "\n\n" + insight.Summary
	if insight.Confidence != "" || insight.SignalQualityLevel != "" {
		summary += fmt.Sprintf("\n\nSignal quality: `%s`; confidence: `%s`.", insight.SignalQualityLevel, insight.Confidence)
	}
	if insight.Caveat != "" {
		summary += "\n\nCaveat: " + insight.Caveat
	}
	return summary
}

func resourceInsight(session *sessionBudgetResult, headroom *planHeadroomResult) presentation.ResourceInsight {
	var signals []presentation.ResourceSignal
	if session != nil && session.Error == "" {
		for _, b := range session.Budgets {
			signals = append(signals, presentation.ResourceSignal{
				Provider:           b.Provider,
				Display:            b.Display,
				Basis:              "session_budget",
				RecommendedAction:  b.RecommendedAction,
				WindowPct:          b.WindowPct,
				Confidence:         b.Confidence,
				SignalQualityLevel: b.SignalQuality.Level,
				Caveat:             b.SignalQuality.Caveat,
			})
		}
	}
	if headroom != nil && headroom.Error == "" {
		for _, report := range headroom.Reports {
			signals = append(signals, presentation.ResourceSignal{
				Provider:           report.Provider,
				Display:            report.Display,
				Basis:              "plan_headroom",
				OverageRisk:        report.OverageRisk,
				SignalQualityLevel: report.SignalQuality.Level,
				Caveat:             report.SignalQuality.Caveat,
			})
		}
	}
	insight := presentation.ForResources(signals)
	return insight
}
