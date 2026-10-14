package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcpgo "go.klarlabs.de/mcp"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/budgets"
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
				Content:           []mcpgo.Content{mcpgo.NewTextContent(markdownPayload(renderResourceGlance(result.Insight)+renderBoundaryAdvice(result.Boundaries)+renderSpendBudgets(result.SpendBudgets), result))},
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

// renderBoundaryAdvice lists the operator's boundaries that hold now. An
// agent in a harness TokenOps cannot refuse reads them here.
func renderBoundaryAdvice(advice []bcap.Advice) string {
	if len(advice) == 0 {
		return ""
	}
	out := "\n\n### Boundaries the operator set\n"
	for _, a := range advice {
		out += fmt.Sprintf("\n- `%s` (%s, %s): %s. %s", a.Rule, a.Action, a.Require, a.Crossed, a.Advice)
	}
	return out
}

// resourceGlance is the glance capability in the tool's shape.
func resourceGlance(ctx context.Context, d PlanDeps) (*resourceGlanceResult, error) {
	deps := d.headroomDeps()
	now := time.Now().UTC()
	g, err := headroom.ComputeGlance(ctx, deps, now)
	if err != nil {
		return nil, err
	}
	p := g.Payload()
	p.Boundaries = bcap.GlanceAdvice(deps.Config, g.Headroom.Reports)
	p.SpendBudgets = budgets.Current("", now)
	return p, nil
}

// renderSpendBudgets lists the operator's budget windows: how much of
// each is used, and whether reaching it notifies or is enforced.
func renderSpendBudgets(rs []budgets.Reading) string {
	if len(rs) == 0 {
		return ""
	}
	out := "\n\n### Spend budgets\n"
	for _, r := range rs {
		mode := "soft: notifies"
		switch {
		case r.Rule != "" && r.Enforced && r.Require == "deny":
			mode = fmt.Sprintf("hard: `%s` refuses at the limit", r.Rule)
		case r.Rule != "" && r.Enforced:
			mode = fmt.Sprintf("hard: `%s` asks at the limit", r.Rule)
		case r.Rule != "":
			mode = fmt.Sprintf("hard, in shadow: `%s` binds nothing yet", r.Rule)
		}
		out += fmt.Sprintf("\n- %s %s: %.0f%% used (%s)", r.Budget, r.Window, r.UsedPct, mode)
	}
	return out
}
