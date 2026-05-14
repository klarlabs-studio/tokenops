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
)

// SignalInputs is the set of observations the quality classifier needs.
// Zero values are valid: the function defaults to the most pessimistic
// reading. The pure-function signature keeps tests trivial.
type SignalInputs struct {
	ProxyEventsInWindow     int64
	MCPPingsInWindow        int64
	ClaudeCodeCacheInWindow int64
	VendorAPIWired          bool
}

// ClassifySignal returns the SignalQuality for a window of observations.
// Decision rules (first match wins):
//
//	vendor /usage wired                                  -> high
//	proxy events >= mcp pings AND proxy events > 0       -> high
//	proxy events between 1 and < mcp pings               -> medium
//	claude-code stats cache observed > 0                 -> medium
//	mcp pings > 0, no proxy/cache events                 -> low
//	no observations at all                               -> low
//
// The thresholds are intentionally coarse: this is a trust signal, not
// a confidence interval. Refine only after the customer interviews show
// operators want more granularity.
func ClassifySignal(in SignalInputs) SignalQuality {
	switch {
	case in.VendorAPIWired:
		return SignalQuality{
			Level:  SignalLevelHigh,
			Source: SignalSourceVendorAPI,
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
			},
		}
	case in.ClaudeCodeCacheInWindow > 0:
		return SignalQuality{
			Level:  SignalLevelMedium,
			Source: SignalSourceClaudeCodeCache,
			Caveat: "Reads ~/.claude/stats-cache.json — an undocumented Claude Code internal cache. Daily granularity only; cannot resolve the 5-hour rolling window.",
			UpgradePaths: []string{
				"route every Claude request through the local proxy for live per-request signal",
				"connect the Anthropic Admin API for metered-API attribution (queued)",
			},
		}
	default:
		return SignalQuality{
			Level:  SignalLevelLow,
			Source: SignalSourceMCPPings,
			Caveat: "TokenOps observes MCP tool invocations only, not your real Claude conversation turns. Treat this as an activity proxy, not a quota meter.",
			UpgradePaths: []string{
				"wire your client base URL to the local proxy (`tokenops provider set ...`)",
				"enable Claude Code stats cache reader (`vendor_usage.claude_code.enabled: true`)",
				"connect a vendor /usage API key (queued)",
			},
		}
	}
}
