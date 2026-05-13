package mcp

import (
	"context"
	"errors"
)

// helpCategory groups MCP tools by typical first-time use so agents
// and operators can navigate the surface without scrolling a flat
// 20-entry tool list. Hick's law: a curated menu beats raw
// enumeration when the catalogue grows past about a dozen items.
type helpCategory struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Tools       []helpTool `json:"tools"`
}

type helpTool struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Example string `json:"example,omitempty"`
}

// helpCatalog is the authoritative grouping the tokenops_help tool
// returns. Adding a new MCP tool requires adding it here so the
// surface stays discoverable; the build-time arch tests are the
// safety net.
var helpCatalog = []helpCategory{
	{
		Name:        "setup",
		Description: "Bind the daemon's data sources. Run these first.",
		Tools: []helpTool{
			{
				Name:    "tokenops_status",
				Summary: "Health + blockers + next_actions. Start here when something is wrong.",
			},
			{
				Name:    "tokenops_config",
				Summary: "Redacted view of the active config snapshot.",
			},
			{
				Name:    "tokenops_version",
				Summary: "Build metadata + eventschema version.",
			},
		},
	},
	{
		Name:        "session",
		Description: "Live rate-limit headroom for MCP-resident sessions (Claude Max / GPT Plus / Copilot / Cursor).",
		Tools: []helpTool{
			{
				Name:    "tokenops_session_budget",
				Summary: "Predict whether this session will hit the rate-limit cap. Returns continue|slow_down|switch_model|wait_for_reset.",
				Example: "Call before starting a long task to decide whether to keep going.",
			},
			{
				Name:    "tokenops_plan_headroom",
				Summary: "Month-to-date consumption + overage risk for every configured plan.",
			},
		},
	},
	{
		Name:        "cost",
		Description: "Token + dollar rollups from the local event store.",
		Tools: []helpTool{
			{
				Name:    "tokenops_spend_summary",
				Summary: "Total requests / tokens / cost over a window. Use `since: '7d'` for the last week.",
			},
			{
				Name:    "tokenops_burn_rate",
				Summary: "Hourly buckets over the last N hours. Default 24.",
			},
			{
				Name:    "tokenops_top_consumers",
				Summary: "Top N spenders grouped by model | provider | workflow | agent.",
			},
			{
				Name:    "tokenops_forecast",
				Summary: "Daily spend forecast horizon_days ahead via Holt's smoothing.",
			},
		},
	},
	{
		Name:        "workflows",
		Description: "Attribution + optimization for multi-step agent runs.",
		Tools: []helpTool{
			{
				Name:    "tokenops_workflow_trace",
				Summary: "Reconstruct a workflow_id trace + run the waste detector.",
			},
			{
				Name:    "tokenops_optimizations",
				Summary: "List optimizer events with quality scores and decisions.",
			},
		},
	},
	{
		Name:        "rules",
		Description: "Operational rule artifacts (CLAUDE.md / AGENTS.md / Cursor / MCP policies) as telemetry.",
		Tools: []helpTool{
			{
				Name:    "tokenops_rules_analyze",
				Summary: "Per-section token cost + density across rule corpora.",
			},
			{
				Name:    "tokenops_rules_conflicts",
				Summary: "Surface redundancy / drift / anti-pattern findings.",
			},
			{
				Name:    "tokenops_rules_compress",
				Summary: "Distill the rule corpus under a quality floor.",
			},
			{
				Name:    "tokenops_rules_inject",
				Summary: "Preview the dynamic rule subset the router picks for a request context.",
			},
		},
	},
	{
		Name:        "debug",
		Description: "Diagnostics for daemon + event flow.",
		Tools: []helpTool{
			{
				Name:    "tokenops_domain_events",
				Summary: "Per-kind in-process domain event counts.",
			},
			{
				Name:    "tokenops_audit",
				Summary: "Query the audit log; daemon-only emission.",
			},
		},
	},
}

// RegisterHelpTool mounts tokenops_help on s. The tool returns the
// helpCatalog above so MCP clients can render a categorised picker.
func RegisterHelpTool(s *Server) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_help").
		Description("Return a curated, category-grouped index of TokenOps MCP tools so agents and operators can find the right tool without enumerating the 20+ flat list.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			return jsonString(map[string]any{
				"categories": helpCatalog,
				"hint":       "tools/list returns the raw schema; this tool curates by first-use order.",
			}), nil
		})
	return nil
}
