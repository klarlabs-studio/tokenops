package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcpgo "go.klarlabs.de/mcp"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/presentation"
)

// resourceGlanceResult is the capability's glance payload, shared with the
// daemon API (ADR 0010 §4).
type resourceGlanceResult = headroom.GlancePayload

func registerResourceGlanceTool(s *Server, d PlanDeps) {
	s.Tool("tokenops_resource_glance").
		Description("Give one compact view of current AI-resource pressure by composing the existing session budget and plan headroom measurements. The insight never changes a model or applies an action; it carries signal quality and caveats, and says uncertain or unavailable when evidence does not support a clear reading.").
		OutputSchema(resourceGlanceResult{}).
		Handler(func(ctx context.Context, _ emptyInput) (mcpgo.StructuredResult, error) {
			result, err := resourceGlance(ctx, d)
			if err != nil {
				return mcpgo.StructuredResult{}, err
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

// resourceGlance is the glance capability in the tool's shape.
func resourceGlance(ctx context.Context, d PlanDeps) (*resourceGlanceResult, error) {
	g, err := headroom.ComputeGlance(ctx, d.headroomDeps(), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return g.Payload(), nil
}
