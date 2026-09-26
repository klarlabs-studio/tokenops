package config

// Per-source configuration hints.
//
// These moved out of the CLI so the MCP surface can answer the same
// question. `vendor-usage status` was CLI-only, which meant an agent could
// see event counts through tokenops_data_sources but could not tell whether
// a source was switched on, or what to switch on — the difference between
// "no data" and "no data because nothing is configured".

// VendorUsageConfigHint routes a SourceTag to its per-source config
// hint. Kept beside the report loop so adding a source is a two-line
// change (helper entry + hint case) rather than an edit to a giant
// literal.
func (c Config) VendorUsageConfigHint(sourceTag string) string {
	switch sourceTag {
	case "claude-code-jsonl":
		return configHintClaudeCodeJSONL(c.VendorUsage.ClaudeCodeJSONL.Enabled)
	case "codex-jsonl":
		return configHintCodexJSONL(c.VendorUsage.CodexJSONL.Enabled)
	case "opencode":
		return configHintOpenCode(c.VendorUsage.OpenCode.Enabled)
	case "claude-code-stats-cache":
		return configHintClaudeCode(c.VendorUsage.ClaudeCode.Enabled)
	case "vendor-usage-anthropic":
		return configHintAnthropic(c.VendorUsage.Anthropic)
	case "github-copilot":
		return configHintCopilot(c.VendorUsage.GitHubCopilot)
	case "cursor-web":
		return configHintCursor(c.VendorUsage.Cursor)
	case "claude-usage-meter":
		return configHintClaudeUsageMeter(c.VendorUsage.ClaudeUsageMeter)
	default:
		return ""
	}
}

func configHintClaudeCode(enabled bool) string {
	if enabled {
		return "DEPRECATED — switch to vendor_usage.claude_code_jsonl"
	}
	return "deprecated; use vendor_usage.claude_code_jsonl instead"
}

func configHintClaudeCodeJSONL(enabled bool) string {
	if enabled {
		return ""
	}
	return "set vendor_usage.claude_code_jsonl.enabled: true (RECOMMENDED — live per-turn signal)"
}

func configHintCodexJSONL(enabled bool) string {
	if enabled {
		return ""
	}
	return "set vendor_usage.codex_jsonl.enabled: true (RECOMMENDED for ChatGPT Plus/Pro users — surfaces OpenAI's official rate_limits 5h + weekly %)"
}

func configHintOpenCode(enabled bool) string {
	if enabled {
		return ""
	}
	return "set vendor_usage.opencode.enabled: true (reads opencode's SQLite store read-only — per-project, multi-provider token attribution)"
}

func configHintCopilot(cfg GitHubCopilotUsageConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.github_copilot.enabled: true (auto-discovers OAuth token from ~/.config/github-copilot)"
	}
	return ""
}

func configHintCursor(cfg CursorUsageConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.cursor.{enabled, cookie, user_id} — extract cookie from the Cursor IDE devtools (WorkosCursorSessionToken)"
	}
	if cfg.Cookie == "" || cfg.UserID == "" {
		return "vendor_usage.cursor enabled but cookie or user_id missing — paste WorkosCursorSessionToken + your user_id from cursor.com devtools"
	}
	return ""
}

func configHintClaudeUsageMeter(cfg ClaudeUsageMeterConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.claude_usage_meter.{enabled, session_key} — paste sessionKey from claude.ai devtools (Application → Cookies). RECOMMENDED for Claude Max users — only source of the official 7-day utilization %"
	}
	if cfg.SessionKey == "" {
		return "vendor_usage.claude_usage_meter enabled but session_key missing — paste from claude.ai devtools"
	}
	return ""
}

func configHintAnthropic(cfg AnthropicUsageConfig) string {
	if !cfg.Enabled {
		return "set vendor_usage.anthropic.enabled: true + an sk-ant-admin-* key"
	}
	if cfg.AdminKey == "" {
		return "vendor_usage.anthropic.admin_key is empty; mint a key in the Claude Console"
	}
	return ""
}
