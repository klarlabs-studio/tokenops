package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcpgo "go.klarlabs.de/mcp"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/budgets"
	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
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
				Content:           []mcpgo.Content{mcpgo.NewTextContent(markdownPayload(renderResourceGlance(result.Insight)+renderRouting(result.Routing)+renderBoundaryAdvice(result.Boundaries)+renderSpendBudgets(result.SpendBudgets)+renderNotConnected(result.Connections), result))},
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

// renderRouting is the one line per plan under pressure telling the agent
// to pass a cheaper model on subagent calls; nothing when no plan is.
func renderRouting(rs []headroom.Routing) string {
	out := ""
	for _, r := range rs {
		out += "\n\n**Routing:** " + r.Advice
	}
	return out
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
	var fromDaemon resourceGlanceResult
	if d.fromDaemon(ctx, "/api/glance", &fromDaemon) {
		return &fromDaemon, nil
	}
	deps := d.headroomDeps()
	now := time.Now().UTC()
	g, err := headroom.ComputeGlance(ctx, deps, now)
	if err != nil {
		return nil, err
	}
	p := g.Payload()
	p.Boundaries = bcap.GlanceAdvice(deps.Config, g.Headroom.Reports)
	p.SpendBudgets = budgets.Current("", now)
	// Without the daemon the readers' health is unknown: a source switched
	// on reads as connected, never as failing.
	p.Connections = connections.Compute(connections.Inputs{Config: deps.Config, Turns: g.Turns,
		Reported: g.Headroom.VendorRead(), Installed: sourceprobe.InstalledTools(d.machine()),
		SignIns: sourceprobe.SignIns(d.machine()), Now: now})
	return p, nil
}

// renderNotConnected tells the agent which tools the operator uses whose
// plan is not read, so it can tell them, with the command for them to
// run; nothing when there is none.
func renderNotConnected(cs []connections.Connection) string {
	detected := connections.Detected(cs)
	if len(detected) == 0 {
		return ""
	}
	out := "\n\n### In use, plan not read\n\nTell the operator; the command needs their credential or their yes, so never run it yourself.\n"
	for _, c := range detected {
		out += "\n- " + c.Summary
		if a := c.Action; a != nil {
			out += fmt.Sprintf(": `%s` (needs %s). Not wanted: `%s`.", a.Command, a.Needs, a.Dismiss)
		}
	}
	return out
}

// renderSpendBudgets lists the operator's budget windows: how much of
// each is left, and whether reaching it notifies or is enforced.
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
		whose := ""
		if r.Provider != "" {
			whose = ", billed by " + budgets.ProviderName(r.Provider)
		}
		out += fmt.Sprintf("\n- %s %s%s: %s (%s)", r.Budget, r.Window, whose, r.LeftWords(), mode)
	}
	return out
}
