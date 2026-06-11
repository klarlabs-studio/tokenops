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
//   - vendor_usage.anthropic_cookie.session_key (claude.ai session)
//   - vendor_usage.cursor.cookie
//   - vendor_usage.github_copilot.oauth_token
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
	mask(&redacted.VendorUsage.AnthropicCookie.SessionKey)
	mask(&redacted.VendorUsage.Cursor.Cookie)
	mask(&redacted.VendorUsage.GitHubCopilot.OAuthToken)
	return redacted
}

// Snapshot returns the redacted configuration as a JSON document.
func (c Config) Snapshot() (json.RawMessage, error) {
	return json.Marshal(c.Redacted())
}
