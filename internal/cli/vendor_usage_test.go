package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// writeSeedConfig drops a minimal valid config at path so the enable
// command can read+mutate+write through the same plumbing the daemon
// uses. Test helper: avoids each subtest hand-rolling fixture YAML.
func writeSeedConfig(t *testing.T, path string) {
	t.Helper()
	if err := writeMutableConfig(path, config.Default()); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// runEnable invokes the enable command in-process so we can assert on
// the persisted config and the printed output without spawning a
// subprocess. Returns the post-mutation config + captured stdout.
func runEnable(t *testing.T, configPath string, args ...string) (config.Config, string, error) {
	t.Helper()
	cmd := newVendorUsageEnableCmd()
	cmd.SetArgs(append(args, "--config-path", configPath))
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	if err != nil {
		return config.Config{}, out.String(), err
	}
	cfg, readErr := readMutableConfig(configPath)
	if readErr != nil {
		t.Fatalf("read post-mutation config: %v", readErr)
	}
	return cfg, out.String(), nil
}

// Sanity: cobra wires the subcommand through the parent so the help
// text + tab-completion surface the new verb.

func TestVendorUsageEnableCmdWiredOnParent(t *testing.T) {
	parent := newVendorUsageCmd()
	var found bool
	for _, sub := range parent.Commands() {
		if sub.Name() == "enable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("enable subcommand not registered on vendor-usage parent")
	}
}

// anthropic-cookie is the silver-bullet source — the only one that
// surfaces Claude Max weekly utilization. Verify (a) refusing without
// a session key, (b) accepting the key, (c) accepting via env var,
// (d) --disable round-trip preserves the previously-set secret.
func TestVendorUsageEnableAnthropicCookie(t *testing.T) {
	t.Run("missing session key errors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeSeedConfig(t, path)
		_, _, err := runEnable(t, path, "anthropic-cookie")
		if err == nil || !strings.Contains(err.Error(), "session-key") {
			t.Fatalf("want missing-key error; got %v", err)
		}
	})
	t.Run("flag persists secret", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeSeedConfig(t, path)
		cfg, out, err := runEnable(t, path,
			"anthropic-cookie", "--session-key", "sk-cookie-xyz", "--org-id", "org-7", "--interval", "3m",
		)
		if err != nil {
			t.Fatalf("enable: %v", err)
		}
		if !cfg.VendorUsage.AnthropicCookie.Enabled {
			t.Error("not enabled")
		}
		if cfg.VendorUsage.AnthropicCookie.SessionKey != "sk-cookie-xyz" {
			t.Errorf("session_key=%q", cfg.VendorUsage.AnthropicCookie.SessionKey)
		}
		if cfg.VendorUsage.AnthropicCookie.OrgID != "org-7" {
			t.Errorf("org_id=%q", cfg.VendorUsage.AnthropicCookie.OrgID)
		}
		if cfg.VendorUsage.AnthropicCookie.Interval != 3*time.Minute {
			t.Errorf("interval=%v", cfg.VendorUsage.AnthropicCookie.Interval)
		}
		if !strings.Contains(out, "enabled vendor_usage.anthropic_cookie") {
			t.Errorf("missing summary line; got %q", out)
		}
	})
	t.Run("env var fallback", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeSeedConfig(t, path)
		t.Setenv("TOKENOPS_ANTHROPIC_COOKIE_SESSION_KEY", "sk-env-key")
		cfg, _, err := runEnable(t, path, "anthropic-cookie")
		if err != nil {
			t.Fatalf("enable: %v", err)
		}
		if cfg.VendorUsage.AnthropicCookie.SessionKey != "sk-env-key" {
			t.Errorf("env fallback ignored; got %q", cfg.VendorUsage.AnthropicCookie.SessionKey)
		}
	})
	t.Run("disable preserves secret", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		writeSeedConfig(t, path)
		if _, _, err := runEnable(t, path, "anthropic-cookie", "--session-key", "sk-keep"); err != nil {
			t.Fatalf("seed enable: %v", err)
		}
		cfg, _, err := runEnable(t, path, "anthropic-cookie", "--disable")
		if err != nil {
			t.Fatalf("disable: %v", err)
		}
		if cfg.VendorUsage.AnthropicCookie.Enabled {
			t.Error("still enabled after --disable")
		}
		if cfg.VendorUsage.AnthropicCookie.SessionKey != "sk-keep" {
			t.Errorf("secret cleared on disable; got %q", cfg.VendorUsage.AnthropicCookie.SessionKey)
		}
	})
}

// Per-source happy-path matrix. One subtest per source covers the
// minimum-args invocation that should land enabled=true with any
// required secret/path populated. Negative paths covered in the
// dedicated anthropic-cookie test above (the others share the same
// envSecret / required-flag plumbing).
func TestVendorUsageEnableSources(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		envKey  string
		envVal  string
		assert  func(t *testing.T, cfg config.Config)
		wantErr string
	}{
		{
			name: "cursor",
			args: []string{"cursor", "--cookie", "ws-cookie", "--user-id", "u-42"},
			assert: func(t *testing.T, cfg config.Config) {
				if !cfg.VendorUsage.Cursor.Enabled {
					t.Error("cursor not enabled")
				}
				if cfg.VendorUsage.Cursor.Cookie != "ws-cookie" || cfg.VendorUsage.Cursor.UserID != "u-42" {
					t.Errorf("cookie=%q user_id=%q", cfg.VendorUsage.Cursor.Cookie, cfg.VendorUsage.Cursor.UserID)
				}
			},
		},
		{
			name:    "cursor without user_id errors",
			args:    []string{"cursor", "--cookie", "ws-cookie"},
			wantErr: "user-id",
		},
		{
			name: "github-copilot needs no secret",
			args: []string{"github-copilot"},
			assert: func(t *testing.T, cfg config.Config) {
				if !cfg.VendorUsage.GitHubCopilot.Enabled {
					t.Error("copilot not enabled")
				}
				if cfg.VendorUsage.GitHubCopilot.OAuthToken != "" {
					t.Errorf("token should stay empty for auto-discovery; got %q", cfg.VendorUsage.GitHubCopilot.OAuthToken)
				}
			},
		},
		{
			name:   "github-copilot accepts env token",
			args:   []string{"github-copilot"},
			envKey: "TOKENOPS_COPILOT_OAUTH_TOKEN", envVal: "ghp-env",
			assert: func(t *testing.T, cfg config.Config) {
				if cfg.VendorUsage.GitHubCopilot.OAuthToken != "ghp-env" {
					t.Errorf("env token not picked up; got %q", cfg.VendorUsage.GitHubCopilot.OAuthToken)
				}
			},
		},
		{
			name: "codex-jsonl",
			args: []string{"codex-jsonl", "--root", "/tmp/codex", "--interval", "45s"},
			assert: func(t *testing.T, cfg config.Config) {
				if !cfg.VendorUsage.CodexJSONL.Enabled || cfg.VendorUsage.CodexJSONL.Root != "/tmp/codex" || cfg.VendorUsage.CodexJSONL.Interval != 45*time.Second {
					t.Errorf("codex jsonl config wrong: %+v", cfg.VendorUsage.CodexJSONL)
				}
			},
		},
		{
			name: "claude-code-jsonl",
			args: []string{"claude-code-jsonl"},
			assert: func(t *testing.T, cfg config.Config) {
				if !cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
					t.Error("claude-code-jsonl not enabled")
				}
			},
		},
		{
			name: "anthropic-admin",
			args: []string{"anthropic-admin", "--admin-key", "sk-ant-admin-zzz", "--bucket-width", "1d"},
			assert: func(t *testing.T, cfg config.Config) {
				if !cfg.VendorUsage.Anthropic.Enabled || cfg.VendorUsage.Anthropic.AdminKey != "sk-ant-admin-zzz" || cfg.VendorUsage.Anthropic.BucketWidth != "1d" {
					t.Errorf("anthropic admin config wrong: %+v", cfg.VendorUsage.Anthropic)
				}
			},
		},
		{
			name:    "anthropic-admin without key errors",
			args:    []string{"anthropic-admin"},
			wantErr: "admin-key",
		},
		{
			name:    "unknown source errors",
			args:    []string{"made-up-source"},
			wantErr: "unknown source",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.envKey != "" {
				t.Setenv(c.envKey, c.envVal)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			writeSeedConfig(t, path)
			cfg, _, err := runEnable(t, path, c.args...)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("want error containing %q; got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("enable: %v", err)
			}
			if c.assert != nil {
				c.assert(t, cfg)
			}
		})
	}
}

// sourceConfigKey is part of the user-facing output ("wrote vendor_usage.X"),
// so the mapping is contract — pin it.
func TestSourceConfigKey(t *testing.T) {
	cases := map[string]string{
		"anthropic-cookie":  "anthropic_cookie",
		"github-copilot":    "github_copilot",
		"codex-jsonl":       "codex_jsonl",
		"claude-code-jsonl": "claude_code_jsonl",
		"anthropic-admin":   "anthropic",
		"cursor":            "cursor",
	}
	for in, want := range cases {
		if got := sourceConfigKey(in); got != want {
			t.Errorf("sourceConfigKey(%q)=%q want %q", in, got, want)
		}
	}
}
