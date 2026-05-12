package config

import "encoding/json"

// SensitiveHeaderPlaceholder is what Snapshot substitutes for every
// OTel header value. The header names are preserved so operators can
// confirm the wiring without exposing tokens.
const SensitiveHeaderPlaceholder = "***REDACTED***"

// Snapshot returns the configuration's wire form as a JSON document
// with sensitive values masked. Adapters (MCP control tool, CLI
// `config show`, dashboard /api/config) consume this — none marshals
// Config directly. Keeping the serialisation pinned in one place lets
// us evolve redaction rules without hunting through adapter code.
//
// Currently redacted:
//   - cfg.OTel.Headers values (tenant tokens, bearer tokens)
func (c Config) Snapshot() (json.RawMessage, error) {
	redacted := c
	if len(redacted.OTel.Headers) > 0 {
		masked := make(map[string]string, len(redacted.OTel.Headers))
		for k := range redacted.OTel.Headers {
			masked[k] = SensitiveHeaderPlaceholder
		}
		redacted.OTel.Headers = masked
	}
	return json.Marshal(redacted)
}
