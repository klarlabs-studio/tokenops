package mcp

import (
	"context"
	"errors"
)

// helpCategory groups MCP tools by typical first-time use so agents
// and operators can navigate the surface without scrolling a flat
// tool list. Hick's law: a curated menu beats raw enumeration when the
// catalogue grows past about a dozen items.
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
// returns. Adding a new MCP tool requires adding it here so the surface
// stays discoverable. TestHelpCatalogCoversEveryRegisteredTool enforces
// that in both directions — this comment once named "arch tests" as the
// safety net when there were none, and eighteen tools went unlisted.
var helpCatalog = []helpCategory{
	{
		Name:        "setup",
		Description: "Bind the daemon's data sources and confirm they are flowing. Run these first.",
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
			{
				Name:    "tokenops_plan_set",
				Summary: "Bind a provider to its subscription plan (claude-max-20x, claude-enterprise, ...).",
			},
			{
				Name:    "tokenops_vendor_usage_setup",
				Summary: "Connect claude.ai's own usage meter; the session key comes from env or config, never the chat.",
			},
			{
				Name:    "tokenops_vendor_usage_status",
				Summary: "Which usage sources are on and how much each ingested recently. Check before trusting a spend or headroom figure.",
			},
			{
				Name:    "tokenops_data_sources",
				Summary: "Event counts by source (proxy, mcp-session, demo, ...), to confirm the math runs on real data, not demo seeds.",
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
				Summary: "Month-to-date consumption + overage risk for every configured plan, including spend against an Enterprise limit.",
			},
		},
	},
	{
		Name:        "cost",
		Description: "Token + dollar rollups from the local event store. On a flat-rate plan real cost is $0; api_equivalent_usd and tokens carry the signal.",
		Tools: []helpTool{
			{
				Name:    "tokenops_spend_summary",
				Summary: "Total requests / tokens / cost / API-equivalent over a window. Use `since: '7d'` for the last week.",
			},
			{
				Name:    "tokenops_burn_rate",
				Summary: "Cost, tokens and API-equivalent over the last N hours (default 24), with the hourly series.",
			},
			{
				Name:    "tokenops_top_consumers",
				Summary: "Top N consumers by model | provider | workflow | agent, ranked by API-equivalent value.",
			},
			{
				Name:    "tokenops_forecast",
				Summary: "Daily spend and token forecast horizon_days ahead via Holt's smoothing.",
			},
			{
				Name:    "tokenops_pricing",
				Summary: "Per-million-token rates TokenOps prices with, and when the card was fetched.",
			},
			{
				Name:    "tokenops_dashboard",
				Summary: "Clickable URL to the local dashboard (cost, tokens, burn-rate charts) served by the daemon.",
			},
		},
	},
	{
		Name:        "routing",
		Description: "Which model a turn should run on. Advice and proposals only change anything once the caller or operator acts on them.",
		Tools: []helpTool{
			{
				Name:    "tokenops_routing_advise",
				Summary: "Recommend stay or switch for a turn, from its task class, the plan window, and the live pricing table. Never applies.",
				Example: `{"instruction":"rename the handler","model":"claude-opus-5"}`,
			},
			{
				Name:    "tokenops_routing_proposals",
				Summary: "Model upgrades the proxy refused because they exceed the preferred model; surface pending ones to the operator.",
			},
			{
				Name:    "tokenops_routing_decide",
				Summary: "Record the operator's approve/deny on a pending proposal. Only once they have actually chosen.",
			},
		},
	},
	{
		Name:        "workflows",
		Description: "Attribution, replay, and the account of work done in multi-step agent runs.",
		Tools: []helpTool{
			{
				Name:    "tokenops_workflow_trace",
				Summary: "Reconstruct a workflow_id trace + run the waste detector.",
			},
			{
				Name:    "tokenops_optimizations",
				Summary: "List optimizer events with quality scores and decisions.",
			},
			{
				Name:    "tokenops_replay",
				Summary: "Replay a session / workflow / agent through the optimizer pipeline to see would-be savings.",
			},
			{
				Name:    "tokenops_story",
				Summary: "Recent work one task at a time: the instruction, turns, tool calls, files touched, and where it went sideways.",
				Example: `{"days":1}`,
			},
		},
	},
	{
		Name:        "coaching",
		Description: "Prompt-quality, session-experience, and context-usage feedback. Privacy-respecting: transcript text is read at scan time and never persisted to the event store.",
		Tools: []helpTool{
			{
				Name:    "tokenops_coach_prompts",
				Summary: "Heuristic scoring of your Claude Code prompting: length distribution, vague/ack/repeat counts, recommendations.",
				Example: `{"since":"7d"}`,
			},
			{
				Name:    "tokenops_agent_dx",
				Summary: "Graded session metrics — turns per instruction, rework, interrupts, first-try rate — with the single highest-leverage change.",
			},
			{
				Name:    "tokenops_fmt_analyze",
				Summary: "What fills your context (Read vs Bash vs prose) and what command-output compression would save on real traffic.",
			},
			{
				Name:    "tokenops_fmt_learn",
				Summary: "Where the output-compression catalog should improve next: missing formatters, over-compression, loss-level hints.",
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
			{
				Name:    "tokenops_rules_bench",
				Summary: "Benchmark rule profiles against scenarios from an inline or on-disk spec.",
			},
		},
	},
	{
		Name:        "control",
		Description: "Mutate the daemon configuration: operating mode, budgets, routing rules, the preferred-model ceiling. Changes persist to config.yaml.",
		Tools: []helpTool{
			{
				Name:    "tokenops_mode",
				Summary: "Get or set passive|active. Active = live routing interventions + background spend watcher.",
				Example: `{"set":"active"}`,
			},
			{
				Name:    "tokenops_budget_set",
				Summary: "Upsert/delete a calendar-window spend limit the active-mode watcher evaluates.",
				Example: `{"name":"weekly-all","window":"weekly","limit_usd":50}`,
			},
			{
				Name:    "tokenops_routing_rule_set",
				Summary: "Upsert/delete a route-X-to-Y rule. Validate via tokenops_replay, enforce via mode=active.",
				Example: `{"provider":"anthropic","from_model":"claude-fable-5*","to_model":"claude-opus-4-8","quality":0.9}`,
			},
			{
				Name:    "tokenops_preferred_model",
				Summary: "Get or set a provider's preferred model: a ceiling that refers pricier routes to the operator.",
			},
		},
	},
	{
		Name:        "evaluation",
		Description: "Quality gates and KPIs computed locally.",
		Tools: []helpTool{
			{
				Name:    "tokenops_eval",
				Summary: "Run the optimizer eval harness; returns the merged report and gate result.",
			},
			{
				Name:    "tokenops_scorecard",
				Summary: "Operator wedge KPI scorecard (FVT, TEU, SAC) from the local event store.",
			},
			{
				Name:    "tokenops_coverage_debt",
				Summary: "Risk-ranked coverage debt from a Go cover profile.",
			},
		},
	},
	{
		Name:        "debug",
		Description: "Diagnostics for daemon + event flow, and this index.",
		Tools: []helpTool{
			{
				Name:    "tokenops_domain_events",
				Summary: "Per-kind in-process domain event counts.",
			},
			{
				Name:    "tokenops_audit",
				Summary: "Query the audit log; daemon-only emission.",
			},
			{
				Name:    "tokenops_help",
				Summary: "This category-grouped index of every TokenOps tool.",
			},
		},
	},
}

// helpResult is the typed payload for tokenops_help.
type helpResult struct {
	Categories []helpCategory `json:"categories"`
	Hint       string         `json:"hint"`
}

// RegisterHelpTool mounts tokenops_help on s. The tool returns the
// helpCatalog above so MCP clients can render a categorised picker.
func RegisterHelpTool(s *Server) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_help").
		Description("Return a curated, category-grouped index of every TokenOps MCP tool (setup, session, cost, routing, workflows, coaching, rules, control, evaluation, debug) so agents and operators can find the right tool without enumerating the flat tools/list.").
		OutputSchema(helpResult{}).
		Handler(func(_ context.Context, _ emptyInput) (helpResult, error) {
			return helpResult{
				Categories: helpCatalog,
				Hint:       "tools/list returns the raw schema; this tool curates by first-use order.",
			}, nil
		})
	return nil
}
