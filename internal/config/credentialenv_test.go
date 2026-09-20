package config

import (
	"os"
	"path/filepath"
	"testing"
)

// SECURITY.md told operators to keep the Anthropic admin key out of
// config.yaml by using "environment substitution". Nothing implemented
// it: Load expanded no `${VAR}`, and applyEnvOverrides covered listen
// addresses and endpoints but not one credential. The only env vars that
// existed belonged to `vendor-usage enable`, which reads them and then
// writes the secret into the file — the opposite of what was promised.
//
// These are the names `vendor-usage enable` already documents, so an
// operator who set them for the CLI gets the daemon behaviour they
// expected all along.
func TestCredentialsCanComeFromTheEnvironment(t *testing.T) {
	cases := []struct {
		env  string
		want func(Config) string
	}{
		{"TOKENOPS_DASHBOARD_ADMIN_TOKEN", func(c Config) string { return c.Dashboard.AdminToken }},
		{"TOKENOPS_ANTHROPIC_ADMIN_KEY", func(c Config) string { return c.VendorUsage.Anthropic.AdminKey }},
		{"TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY", func(c Config) string {
			return c.VendorUsage.ClaudeUsageMeter.SessionKey
		}},
		{"TOKENOPS_CURSOR_COOKIE", func(c Config) string { return c.VendorUsage.Cursor.Cookie }},
		{"TOKENOPS_COPILOT_OAUTH_TOKEN", func(c Config) string { return c.VendorUsage.GitHubCopilot.OAuthToken }},
	}

	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv(tc.env, "from-environment")
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := tc.want(cfg); got != "from-environment" {
				t.Errorf("%s was ignored; field = %q", tc.env, got)
			}
		})
	}
}

// Environment always wins, which is what makes it useful: the operator
// can leave a stale value in the file and override it for one run
// without rewriting config.yaml.
func TestCredentialEnvBeatsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "vendor_usage:\n  anthropic:\n    admin_key: from-file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Setenv("TOKENOPS_ANTHROPIC_ADMIN_KEY", "from-environment")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VendorUsage.Anthropic.AdminKey != "from-environment" {
		t.Errorf("file value won over the environment: %q", cfg.VendorUsage.Anthropic.AdminKey)
	}
}

// An unset variable must leave the file's value alone rather than
// blanking it — otherwise setting one credential in the environment
// would silently disable the others.
func TestUnsetCredentialEnvLeavesTheFileValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "vendor_usage:\n  anthropic:\n    admin_key: from-file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Setenv("TOKENOPS_ANTHROPIC_ADMIN_KEY", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VendorUsage.Anthropic.AdminKey != "from-file" {
		t.Errorf("an empty env var wiped the configured key: %q", cfg.VendorUsage.Anthropic.AdminKey)
	}
}

// A credential supplied by the environment is still a credential. It
// must not reach `config show` or /api/config just because it did not
// come from the file.
func TestCredentialFromEnvIsStillRedacted(t *testing.T) {
	t.Setenv("TOKENOPS_ANTHROPIC_ADMIN_KEY", "sk-ant-admin-secret")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Redacted().VendorUsage.Anthropic.AdminKey; got != SensitiveHeaderPlaceholder {
		t.Errorf("env-supplied key leaked through Redacted: %q", got)
	}
}

// Every field reachable from the environment is a credential, so every
// one of them must also be masked by Redacted. The two lists are
// maintained by hand in different files; this is what keeps them
// agreeing.
func TestEveryCredentialEnvFieldIsRedacted(t *testing.T) {
	for _, v := range CredentialEnvVars() {
		t.Run(v.Name, func(t *testing.T) {
			if v.Name == "" || v.Field == nil || v.Describes == "" {
				t.Fatalf("malformed entry: %+v", v)
			}
			cfg := Default()
			*v.Field(&cfg) = "a-real-secret"

			red := cfg.Redacted()
			if got := *v.Field(&red); got != SensitiveHeaderPlaceholder {
				t.Errorf("%s reaches a field Redacted does not mask (got %q); "+
					"add it to Config.Redacted", v.Name, got)
			}
		})
	}
}

// Two entries pointing at the same field, or a duplicated variable name,
// would make one of them dead without anything saying so.
func TestCredentialEnvVarsAreDistinct(t *testing.T) {
	cfg := Default()
	names := map[string]bool{}
	fields := map[*string]string{}
	for _, v := range CredentialEnvVars() {
		if names[v.Name] {
			t.Errorf("duplicate env var %s", v.Name)
		}
		names[v.Name] = true

		p := v.Field(&cfg)
		if prev, ok := fields[p]; ok {
			t.Errorf("%s and %s point at the same field", prev, v.Name)
		}
		fields[p] = v.Name
	}
}
