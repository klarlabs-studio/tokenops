package plans

import "testing"

func TestClassifySignalNoObservations(t *testing.T) {
	q := ClassifySignal(SignalInputs{})
	if q.Level != SignalLevelLow {
		t.Errorf("level=%q want low", q.Level)
	}
	if q.Source != SignalSourceMCPPings {
		t.Errorf("source=%q want mcp_tool_pings", q.Source)
	}
	if q.Caveat == "" {
		t.Error("low-quality signal must carry a caveat so callers can render it")
	}
}

func TestClassifySignalMCPPingsOnly(t *testing.T) {
	q := ClassifySignal(SignalInputs{MCPPingsInWindow: 12})
	if q.Level != SignalLevelLow {
		t.Errorf("mcp-only must stay low, got %q", q.Level)
	}
}

func TestClassifySignalProxyDominant(t *testing.T) {
	q := ClassifySignal(SignalInputs{ProxyEventsInWindow: 50, MCPPingsInWindow: 5})
	if q.Level != SignalLevelHigh {
		t.Errorf("proxy-dominant must be high, got %q", q.Level)
	}
	if q.Source != SignalSourceProxy {
		t.Errorf("source=%q want proxy_traffic", q.Source)
	}
}

func TestClassifySignalProxyPartial(t *testing.T) {
	q := ClassifySignal(SignalInputs{ProxyEventsInWindow: 5, MCPPingsInWindow: 50})
	if q.Level != SignalLevelMedium {
		t.Errorf("partial coverage must be medium, got %q", q.Level)
	}
	if len(q.UpgradePaths) == 0 {
		t.Error("medium quality must suggest upgrade paths")
	}
}

// Claude Code stats cache observed but no proxy traffic → medium.
// Mentions the cache caveat so consumers know the granularity limit.
func TestClassifySignalClaudeCodeCacheOnly(t *testing.T) {
	q := ClassifySignal(SignalInputs{ClaudeCodeCacheInWindow: 5, MCPPingsInWindow: 100})
	if q.Level != SignalLevelMedium {
		t.Errorf("claude-code-cache-only must be medium, got %q", q.Level)
	}
	if q.Source != SignalSourceClaudeCodeCache {
		t.Errorf("source=%q want claude_code_stats_cache", q.Source)
	}
	if q.Caveat == "" {
		t.Error("cache-driven signal must carry a caveat about daily granularity")
	}
}

// Proxy traffic still wins over the cache reader — proxy is per-request
// observation; cache is daily roll-ups.
func TestClassifySignalProxyBeatsClaudeCodeCache(t *testing.T) {
	q := ClassifySignal(SignalInputs{
		ProxyEventsInWindow:     50,
		MCPPingsInWindow:        10,
		ClaudeCodeCacheInWindow: 5,
	})
	if q.Source != SignalSourceProxy {
		t.Errorf("proxy must trump cache; got source=%q", q.Source)
	}
}

// JSONL observation is HIGH confidence — real per-turn data from
// Claude Code's own conversation record. Should beat proxy + mcp.
func TestClassifySignalClaudeCodeJSONLIsHigh(t *testing.T) {
	q := ClassifySignal(SignalInputs{ClaudeCodeJSONLInWindow: 5, MCPPingsInWindow: 100, ProxyEventsInWindow: 2})
	if q.Level != SignalLevelHigh {
		t.Errorf("jsonl observed must be high; got %q", q.Level)
	}
	if q.Source != SignalSourceClaudeCodeJSONL {
		t.Errorf("source = %q; want claude_code_jsonl", q.Source)
	}
	if q.Caveat == "" {
		t.Error("high-jsonl signal still carries a caveat for transparency")
	}
}

// Copilot observation is HIGH confidence — calls Copilot's internal
// user endpoint with the OAuth token Copilot IDE plugins use.
func TestClassifySignalCopilotIsHigh(t *testing.T) {
	q := ClassifySignal(SignalInputs{CopilotInWindow: 5, MCPPingsInWindow: 100})
	if q.Level != SignalLevelHigh {
		t.Errorf("copilot must be high; got %q", q.Level)
	}
	if q.Source != SignalSourceCopilot {
		t.Errorf("source = %q; want github_copilot", q.Source)
	}
}

// Cursor observation is HIGH confidence — cookie scrape of an
// undocumented but stable endpoint.
func TestClassifySignalCursorIsHigh(t *testing.T) {
	q := ClassifySignal(SignalInputs{CursorInWindow: 5})
	if q.Level != SignalLevelHigh {
		t.Errorf("cursor must be high; got %q", q.Level)
	}
	if q.Source != SignalSourceCursor {
		t.Errorf("source = %q; want cursor_web", q.Source)
	}
}

// Anthropic cookie observation is HIGH confidence — same data
// Anthropic's own UI shows. Beats the local JSONL parser when both
// are present because the cookie scrape is server-authoritative for
// the Max-plan weekly + 5h windows the JSONL can't resolve.
func TestClassifySignalAnthropicCookieIsHigh(t *testing.T) {
	q := ClassifySignal(SignalInputs{AnthropicCookieInWindow: 1})
	if q.Level != SignalLevelHigh {
		t.Errorf("anthropic-cookie must be high; got %q", q.Level)
	}
	if q.Source != SignalSourceAnthropicCookie {
		t.Errorf("source = %q; want anthropic_cookie", q.Source)
	}
}

// AnthropicCookie wins over ClaudeCodeJSONL when both are present.
func TestClassifySignalCookieBeatsJSONL(t *testing.T) {
	q := ClassifySignal(SignalInputs{AnthropicCookieInWindow: 1, ClaudeCodeJSONLInWindow: 100})
	if q.Source != SignalSourceAnthropicCookie {
		t.Errorf("cookie must trump jsonl; got source=%q", q.Source)
	}
}

// Vendor API still wins over jsonl when both are available — vendor
// data is server-authoritative.
func TestClassifySignalVendorAPIBeatsJSONL(t *testing.T) {
	q := ClassifySignal(SignalInputs{VendorAPIWired: true, ClaudeCodeJSONLInWindow: 100})
	if q.Source != SignalSourceVendorAPI {
		t.Errorf("vendor-api must trump jsonl; got %q", q.Source)
	}
}

// Codex JSONL is HIGH confidence and stays separate from Claude Code
// — different provider, different source tag, different caveat.
func TestClassifySignalCodexJSONLIsHigh(t *testing.T) {
	q := ClassifySignal(SignalInputs{CodexJSONLInWindow: 5})
	if q.Level != SignalLevelHigh {
		t.Errorf("codex jsonl must be high; got %q", q.Level)
	}
	if q.Source != SignalSourceCodexJSONL {
		t.Errorf("source = %q; want codex_jsonl", q.Source)
	}
}

func TestClassifySignalVendorWiredTrumpsAll(t *testing.T) {
	q := ClassifySignal(SignalInputs{
		ProxyEventsInWindow: 1000, MCPPingsInWindow: 0, VendorAPIWired: true,
	})
	if q.Level != SignalLevelHigh {
		t.Errorf("vendor wired must be high, got %q", q.Level)
	}
	if q.Source != SignalSourceVendorAPI {
		t.Errorf("source=%q want vendor_usage_api", q.Source)
	}
}
