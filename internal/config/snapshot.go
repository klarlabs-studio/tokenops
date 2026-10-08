package config

import "encoding/json"

// SensitiveHeaderPlaceholder is what Redacted substitutes for every
// secret value. Field names are preserved so operators can confirm the
// wiring without exposing tokens.
const SensitiveHeaderPlaceholder = "***REDACTED***"

// Redacted returns a copy of the configuration with every secret
// masked. All operator-facing serialisations (MCP control tool, CLI
// `config show`, dashboard /api/config) must marshal this — never the
// raw Config. Keeping the rules in one place lets redaction evolve
// without hunting through adapter code.
//
// Redacted fields:
//   - otel.headers values (tenant / bearer tokens)
//   - dashboard.admin_token
//   - vendor_usage.anthropic.admin_key (sk-ant-admin-*)
//   - vendor_usage.claude_usage_meter.session_key (claude.ai session)
//   - vendor_usage.claude_usage_meter.clearance (Cloudflare session proof)
//   - vendor_usage.claude_usage_meter.browser_cookies values
//   - vendor_usage.cursor.cookie
//   - vendor_usage.github_copilot.oauth_token
//   - vendor_usage.accounts.credentials keys
func (c Config) Redacted() Config {
	redacted := c
	if len(redacted.OTel.Headers) > 0 {
		masked := make(map[string]string, len(redacted.OTel.Headers))
		for k := range redacted.OTel.Headers {
			masked[k] = SensitiveHeaderPlaceholder
		}
		redacted.OTel.Headers = masked
	}
	mask := func(s *string) {
		if *s != "" {
			*s = SensitiveHeaderPlaceholder
		}
	}
	mask(&redacted.Dashboard.AdminToken)
	mask(&redacted.VendorUsage.Anthropic.AdminKey)
	mask(&redacted.VendorUsage.ClaudeUsageMeter.SessionKey)
	mask(&redacted.VendorUsage.ClaudeUsageMeter.Clearance)
	if len(redacted.VendorUsage.ClaudeUsageMeter.BrowserCookies) > 0 {
		masked := make(map[string]string, len(redacted.VendorUsage.ClaudeUsageMeter.BrowserCookies))
		for k := range redacted.VendorUsage.ClaudeUsageMeter.BrowserCookies {
			masked[k] = SensitiveHeaderPlaceholder
		}
		redacted.VendorUsage.ClaudeUsageMeter.BrowserCookies = masked
	}
	mask(&redacted.VendorUsage.Cursor.Cookie)
	mask(&redacted.VendorUsage.GitHubCopilot.OAuthToken)
	if len(redacted.VendorUsage.Accounts.Credentials) > 0 {
		masked := make(map[string]AccountCredential, len(redacted.VendorUsage.Accounts.Credentials))
		for k, v := range redacted.VendorUsage.Accounts.Credentials {
			mask(&v.Key)
			masked[k] = v
		}
		redacted.VendorUsage.Accounts.Credentials = masked
	}
	return redacted
}

// Snapshot returns the redacted configuration as a JSON document.
func (c Config) Snapshot() (json.RawMessage, error) {
	return json.Marshal(c.Redacted())
}
