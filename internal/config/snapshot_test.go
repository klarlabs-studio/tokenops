package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSnapshotRedactsOTelHeaders(t *testing.T) {
	c := Config{
		Listen: "127.0.0.1:7878",
		OTel: OTelConfig{
			Enabled:  true,
			Endpoint: "https://collector.example.com",
			Headers: map[string]string{
				"X-Tenant-Token": "secret-bearer-abc123",
				"Authorization":  "Bearer raw-secret-key",
			},
		},
	}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "secret-bearer-abc123") || strings.Contains(s, "raw-secret-key") {
		t.Fatalf("snapshot leaked header value: %s", s)
	}
	if !strings.Contains(s, SensitiveHeaderPlaceholder) {
		t.Errorf("snapshot missing placeholder: %s", s)
	}
	// Header names preserved.
	if !strings.Contains(s, "X-Tenant-Token") {
		t.Errorf("header name dropped: %s", s)
	}
}

func TestSnapshotPreservesOriginal(t *testing.T) {
	c := Config{OTel: OTelConfig{Headers: map[string]string{"k": "secret"}}}
	if _, err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if c.OTel.Headers["k"] != "secret" {
		t.Errorf("snapshot mutated input: %v", c.OTel.Headers)
	}
}

func TestSnapshotEmptyHeadersStable(t *testing.T) {
	c := Config{Listen: "x"}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
}

// Every secret field must be masked by Redacted — the CLI, MCP, and
// dashboard all serialize this copy. A new secret field that isn't
// masked here leaks via `tokenops config show`.
func TestRedactedMasksAllSecrets(t *testing.T) {
	cfg := Default()
	cfg.Dashboard.AdminToken = "tok"
	cfg.VendorUsage.Anthropic.AdminKey = "sk-ant-admin-x"
	cfg.VendorUsage.AnthropicCookie.SessionKey = "sk-ant-sid02-x"
	cfg.VendorUsage.Cursor.Cookie = "cookie"
	cfg.VendorUsage.GitHubCopilot.OAuthToken = "gho_x"

	r := cfg.Redacted()
	for name, got := range map[string]string{
		"dashboard.admin_token":        r.Dashboard.AdminToken,
		"anthropic.admin_key":          r.VendorUsage.Anthropic.AdminKey,
		"anthropic_cookie.session_key": r.VendorUsage.AnthropicCookie.SessionKey,
		"cursor.cookie":                r.VendorUsage.Cursor.Cookie,
		"github_copilot.oauth_token":   r.VendorUsage.GitHubCopilot.OAuthToken,
	} {
		if got != SensitiveHeaderPlaceholder {
			t.Errorf("%s not redacted: %q", name, got)
		}
	}
	// Original untouched; empty secrets stay empty (no placeholder noise).
	if cfg.VendorUsage.AnthropicCookie.SessionKey != "sk-ant-sid02-x" {
		t.Error("Redacted mutated the original")
	}
	if Default().Redacted().Dashboard.AdminToken != "" {
		t.Error("empty secret gained a placeholder")
	}
}
