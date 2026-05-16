package plans

// SignalQuality types the trust the operator can place in a session
// budget or plan headroom report. The structure is intentionally a
// closed enum on Level so agents (Claude Code, Cursor) can branch
// without parsing prose, plus a free-form Caveat that humans render.
//
// Three sources, ranked by faithfulness to the operator's real Claude
// consumption:
//
//	mcp_tool_pings   — counts tokenops_* invocations from the MCP host.
//	                   activity proxy; does NOT see the user's real
//	                   conversation turns with Claude. low quality.
//	proxy_traffic    — events flow through the local TokenOps proxy.
//	                   captures every request the proxy mediates. high
//	                   quality when the user has actually wired their
//	                   client base URL to the proxy.
//	vendor_usage_api — TokenOps polls Anthropic / OpenAI / Cursor usage
//	                   endpoints with the user's credentials. highest
//	                   quality; not yet implemented (queued).
//
// The honest default for plan-based subscribers is mcp_tool_pings, so
// every default response carries a Caveat warning the consumer that
// this is a heuristic.
type SignalQuality struct {
	Level        string   `json:"level"`
	Source       string   `json:"source"`
	Caveat       string   `json:"caveat,omitempty"`
	UpgradePaths []string `json:"upgrade_paths,omitempty"`
}

// Signal level + source constants. Closed sets — never compare against
// hard-coded strings outside this package.
const (
	SignalLevelLow    = "low"
	SignalLevelMedium = "medium"
	SignalLevelHigh   = "high"

	SignalSourceMCPPings        = "mcp_tool_pings"
	SignalSourceProxy           = "proxy_traffic"
	SignalSourceVendorAPI       = "vendor_usage_api"
	SignalSourceClaudeCodeCache = "claude_code_stats_cache"
	SignalSourceClaudeCodeJSONL = "claude_code_jsonl"
	SignalSourceCodexJSONL      = "codex_jsonl"
	SignalSourceCopilot         = "github_copilot"
	SignalSourceCursor          = "cursor_web"
)

// SignalInputs is the set of observations the quality classifier needs.
// Zero values are valid: the function defaults to the most pessimistic
// reading. The pure-function signature keeps tests trivial.
type SignalInputs struct {
	ProxyEventsInWindow     int64
	MCPPingsInWindow        int64
	ClaudeCodeCacheInWindow int64
	ClaudeCodeJSONLInWindow int64
	CodexJSONLInWindow      int64
	CopilotInWindow         int64
	CursorInWindow          int64
	VendorAPIWired          bool
}

// ClassifySignal returns the SignalQuality for a window of observations.
// Decision rules (first match wins):
//
//	vendor /usage wired                                  -> high
//	claude-code jsonl observed > 0                       -> high (real per-turn)
//	proxy events >= mcp pings AND proxy events > 0       -> high
//	proxy events between 1 and < mcp pings               -> medium
//	claude-code stats cache observed > 0                 -> medium (stale, deprecated)
//	mcp pings > 0, no proxy/jsonl/cache events           -> low
//	no observations at all                               -> low
//
// The thresholds are intentionally coarse: this is a trust signal, not
// a confidence interval.
func ClassifySignal(in SignalInputs) SignalQuality {
	switch {
	case in.VendorAPIWired:
		return SignalQuality{
			Level:  SignalLevelHigh,
			Source: SignalSourceVendorAPI,
		}
	case in.ClaudeCodeJSONLInWindow > 0:
		return SignalQuality{
			Level:  SignalLevelHigh,
			Source: SignalSourceClaudeCodeJSONL,
			Caveat: "Reads ~/.claude/projects/**/*.jsonl — Claude Code's live per-turn conversation record. Real input/output/cache token counts; lags the running turn by seconds at most.",
		}
	case in.CodexJSONLInWindow > 0:
		return SignalQuality{
			Level:  SignalLevelHigh,
			Source: SignalSourceCodexJSONL,
			Caveat: "Reads ~/.codex/sessions/**/*.jsonl — Codex CLI's per-turn token_count records. Carries OpenAI's authoritative rate_limits block (5h primary + weekly secondary used_percent + resets_at).",
		}
	case in.CopilotInWindow > 0:
		return SignalQuality{
			Level:  SignalLevelHigh,
			Source: SignalSourceCopilot,
			Caveat: "Calls api.github.com/copilot_internal/user with the OAuth token Copilot IDE plugins use. Returns live quota_snapshots (percent_remaining + entitlement + reset date). Undocumented endpoint but stable since 2022 and shared with every Copilot IDE integration.",
		}
	case in.CursorInWindow > 0:
		return SignalQuality{
			Level:  SignalLevelHigh,
			Source: SignalSourceCursor,
			Caveat: "Calls cursor.com/api/usage with the WorkosCursorSessionToken cookie the IDE uses. Same data the IDE's status-bar reads; undocumented endpoint, contract may shift without notice.",
		}
	case in.ProxyEventsInWindow > 0 && in.ProxyEventsInWindow >= in.MCPPingsInWindow:
		return SignalQuality{
			Level:  SignalLevelHigh,
			Source: SignalSourceProxy,
			Caveat: "Headroom math reflects real proxy-observed Claude requests in this window.",
		}
	case in.ProxyEventsInWindow > 0:
		return SignalQuality{
			Level:  SignalLevelMedium,
			Source: SignalSourceProxy,
			Caveat: "Partial proxy coverage: some Claude turns flow through TokenOps, the rest are inferred from MCP-ping activity.",
			UpgradePaths: []string{
				"route every client request through the proxy for full coverage",
				"enable the Claude Code JSONL reader (`vendor_usage.claude_code_jsonl.enabled: true`)",
			},
		}
	case in.ClaudeCodeCacheInWindow > 0:
		return SignalQuality{
			Level:  SignalLevelMedium,
			Source: SignalSourceClaudeCodeCache,
			Caveat: "Reads ~/.claude/stats-cache.json — DEPRECATED, lags by days on active users. Switch to vendor_usage.claude_code_jsonl for live data.",
			UpgradePaths: []string{
				"enable the Claude Code JSONL reader (`vendor_usage.claude_code_jsonl.enabled: true`)",
			},
		}
	default:
		return SignalQuality{
			Level:  SignalLevelLow,
			Source: SignalSourceMCPPings,
			Caveat: "TokenOps observes MCP tool invocations only, not your real Claude conversation turns. Treat this as an activity proxy, not a quota meter.",
			UpgradePaths: []string{
				"enable the Claude Code JSONL reader (`vendor_usage.claude_code_jsonl.enabled: true`) — recommended, free, no extra credentials",
				"wire your client base URL to the local proxy (`tokenops provider set ...`)",
				"connect a vendor /usage API key (queued)",
			},
		}
	}
}
