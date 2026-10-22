package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// runEnableWithStdin runs enable with stdin fed from in and returns the
// persisted config and stderr apart from stdout, so a test can tell a
// warning from the report.
func runEnableWithStdin(t *testing.T, in string, args ...string) (cfg config.Config, stderr string, err error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeSeedConfig(t, path)
	cmd := withConfigFlag(newVendorUsageEnableCmd())
	cmd.SetArgs(append(args, "--config", path, "--no-restart"))
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(in))
	if err = cmd.Execute(); err != nil {
		return config.Config{}, errBuf.String(), err
	}
	cfg, err = readMutableConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, errBuf.String(), nil
}

// "-" reads a secret flag's value from stdin (a hidden prompt on a
// terminal), so it never appears in ps output or shell history.
func TestEnableReadsSecretFlagsFromStdin(t *testing.T) {
	cases := []struct {
		name string
		args []string
		got  func(config.Config) string
	}{
		{"session-key", []string{"claude-subscription", "--session-key", "-"}, func(c config.Config) string { return c.VendorUsage.ClaudeUsageMeter.SessionKey }},
		{"cookie", []string{"cursor", "--cookie", "-", "--user-id", "u1"}, func(c config.Config) string { return c.VendorUsage.Cursor.Cookie }},
		{"admin-key", []string{"anthropic-admin", "--admin-key", "-"}, func(c config.Config) string { return c.VendorUsage.Anthropic.AdminKey }},
		{"oauth-token", []string{"github-copilot", "--oauth-token", "-"}, func(c config.Config) string { return c.VendorUsage.GitHubCopilot.OAuthToken }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, stderr, err := runEnableWithStdin(t, "  piped-secret-value\n", tc.args...)
			if err != nil {
				t.Fatalf("enable: %v", err)
			}
			if got := tc.got(cfg); got != "piped-secret-value" {
				t.Errorf("persisted %q; want the trimmed stdin value", got)
			}
			if strings.Contains(stderr, "deprecated") {
				t.Errorf("stdin path warned: %s", stderr)
			}
		})
	}
}

// An empty stdin is refused rather than writing an empty secret.
func TestEnableRefusesAnEmptyStdinSecret(t *testing.T) {
	_, _, err := runEnableWithStdin(t, "\n", "cursor", "--cookie", "-", "--user-id", "u1")
	if err == nil || !strings.Contains(err.Error(), "--cookie") {
		t.Fatalf("err = %v; want an empty --cookie refused", err)
	}
}

// The env var path needs no flag and prints no warning.
func TestEnableTakesSecretsFromTheEnvironmentQuietly(t *testing.T) {
	t.Setenv("TOKENOPS_CURSOR_COOKIE", "env-cookie")
	cfg, stderr, err := runEnableWithStdin(t, "", "cursor", "--user-id", "u1")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if cfg.VendorUsage.Cursor.Cookie != "env-cookie" {
		t.Errorf("cookie = %q; want the env value", cfg.VendorUsage.Cursor.Cookie)
	}
	if stderr != "" {
		t.Errorf("env path wrote to stderr: %s", stderr)
	}
}

// A secret given literally on the command line still works, but warns
// on stderr that it is visible to other users and kept in history, and
// the warning never repeats the value.
func TestEnableWarnsAboutALiteralSecretFlag(t *testing.T) {
	cfg, stderr, err := runEnableWithStdin(t, "", "cursor", "--cookie", "literal-cookie-value", "--user-id", "u1")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if cfg.VendorUsage.Cursor.Cookie != "literal-cookie-value" {
		t.Errorf("cookie = %q; the flag must keep working", cfg.VendorUsage.Cursor.Cookie)
	}
	for _, want := range []string{"--cookie", "deprecated", "ps", "history", "--cookie -", "TOKENOPS_CURSOR_COOKIE"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("warning omits %q: %s", want, stderr)
		}
	}
	if strings.Contains(stderr, "literal-cookie-value") {
		t.Errorf("warning repeats the secret: %s", stderr)
	}
}

// The help shows the safe forms, never a secret typed on the command line.
func TestEnableHelpShowsNoLiteralSecret(t *testing.T) {
	long := newVendorUsageEnableCmd().Long
	if strings.Contains(long, "--cookie ey") {
		t.Errorf("help still shows a literal cookie:\n%s", long)
	}
	if !strings.Contains(long, "--cookie -") {
		t.Errorf("help does not show the stdin form:\n%s", long)
	}
}
