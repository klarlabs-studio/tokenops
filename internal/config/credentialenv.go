package config

import "os"

// CredentialEnv binds one credential field to the environment variable
// that can supply it without the secret ever reaching config.yaml.
type CredentialEnv struct {
	// Name is the environment variable operators set.
	Name string
	// Field returns a pointer to the credential inside cfg.
	Field func(cfg *Config) *string
	// Describes the credential for `tokenops config` and SECURITY.md.
	Describes string
}

// CredentialEnvVars is the single list of credentials that can come from
// the environment instead of the config file.
//
// SECURITY.md has told operators since v0.10.2 to keep the Anthropic
// admin key out of config.yaml using "environment substitution". Nothing
// implemented that: Load expanded no `${VAR}`, and applyEnvOverrides
// covered listen addresses, endpoints and plan names without touching a
// single secret. The only environment variables that existed belonged to
// `vendor-usage enable`, which reads one and then writes the secret into
// the file — the opposite of the promise.
//
// The names match the ones `vendor-usage enable` already documents, so an
// operator who exported them for the CLI now gets the daemon behaviour
// they reasonably assumed they already had.
//
// Every entry here must also be masked by Redacted: a credential is no
// less a credential for having arrived through the environment.
func CredentialEnvVars() []CredentialEnv {
	return []CredentialEnv{
		{
			Name:      "TOKENOPS_DASHBOARD_ADMIN_TOKEN",
			Field:     func(c *Config) *string { return &c.Dashboard.AdminToken },
			Describes: "dashboard admin token",
		},
		{
			Name:      "TOKENOPS_ANTHROPIC_ADMIN_KEY",
			Field:     func(c *Config) *string { return &c.VendorUsage.Anthropic.AdminKey },
			Describes: "Anthropic admin key (sk-ant-admin-*)",
		},
		{
			Name:      "TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY",
			Field:     func(c *Config) *string { return &c.VendorUsage.ClaudeUsageMeter.SessionKey },
			Describes: "claude.ai session cookie",
		},
		{
			Name:      "TOKENOPS_CURSOR_COOKIE",
			Field:     func(c *Config) *string { return &c.VendorUsage.Cursor.Cookie },
			Describes: "cursor.com session cookie",
		},
		{
			Name:      "TOKENOPS_COPILOT_OAUTH_TOKEN",
			Field:     func(c *Config) *string { return &c.VendorUsage.GitHubCopilot.OAuthToken },
			Describes: "GitHub Copilot OAuth token",
		},
	}
}

// applyCredentialEnv overlays credentials supplied by the environment.
//
// An unset or empty variable leaves the configured value alone. Treating
// empty as "clear this" would mean exporting one credential silently
// disabled the others, which is a worse failure than the one this fixes:
// the daemon would poll with no key and report nothing, the exact silent
// failure `vendor-usage setup` was built to prevent.
func applyCredentialEnv(cfg *Config) {
	for _, v := range CredentialEnvVars() {
		if val := os.Getenv(v.Name); val != "" {
			*v.Field(cfg) = val
		}
	}
}
