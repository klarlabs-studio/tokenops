package config

import (
	"strings"
	"testing"
)

// configHintAnthropic reports the missing-piece message operators
// need to see — empty when fully configured, specific otherwise.
func TestConfigHintAnthropic(t *testing.T) {
	cases := []struct {
		name string
		cfg  AnthropicUsageConfig
		want string
	}{
		{"disabled", AnthropicUsageConfig{}, "set vendor_usage.anthropic.enabled: true + an sk-ant-admin-* key"},
		{"enabled but keyless", AnthropicUsageConfig{Enabled: true}, "vendor_usage.anthropic.admin_key is empty; mint a key in the Claude Console"},
		{"fully configured", AnthropicUsageConfig{Enabled: true, AdminKey: "sk-ant-admin-x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := configHintAnthropic(c.cfg); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// configHintClaudeCode signals "deprecated" in both states — the
// stats-cache source is being phased out in favour of the JSONL
// reader. Enabled = deprecation warning; disabled = pointer at the
// replacement.
func TestConfigHintClaudeCode(t *testing.T) {
	if got := configHintClaudeCode(true); !strings.Contains(strings.ToLower(got), "deprecat") {
		t.Errorf("enabled state should warn of deprecation; got %q", got)
	}
	if got := configHintClaudeCode(false); got == "" {
		t.Errorf("disabled hint should be set")
	}
}

// configHintClaudeCodeJSONL is symmetric — empty when on, hint with
// "recommended" framing when off (this is the v0.12+ default path).
func TestConfigHintClaudeCodeJSONL(t *testing.T) {
	if got := configHintClaudeCodeJSONL(true); got != "" {
		t.Errorf("enabled hint should be empty; got %q", got)
	}
	if got := configHintClaudeCodeJSONL(false); got == "" {
		t.Errorf("disabled hint should be set")
	}
}

// Gemini CLI stopped serving Google AI Pro on 2026-06-18. With that plan
// bound, the Gemini CLI row says so and points at Antigravity, whether
// the reader is on or off, and under the plan's old name too.
func TestGeminiCLIHintPointsAConsumerPlanAtAntigravity(t *testing.T) {
	for _, plan := range []string{"gemini-ai-pro", "gemini-ai-premium"} {
		for _, enabled := range []bool{false, true} {
			cfg := Default()
			cfg.Plans = map[string]string{"gemini": plan}
			cfg.VendorUsage.GeminiCLI.Enabled = enabled
			got := cfg.VendorUsageConfigHint("gemini-cli")
			for _, want := range []string{"Google AI Pro", "2026-06-18", "Antigravity", "vendor-usage setup antigravity --use-app-login"} {
				if !strings.Contains(got, want) {
					t.Errorf("plan %s, enabled %v: hint %q lacks %q", plan, enabled, got, want)
				}
			}
		}
	}
	cfg := Default()
	cfg.Plans = map[string]string{"gemini": "gemini-ai-pro"}
	cfg.VendorUsage.Grants = map[string]AppLoginGrant{"antigravity": {App: "Antigravity"}}
	if got := cfg.VendorUsageConfigHint("gemini-cli"); !strings.Contains(got, "reads Antigravity's own sign-in") {
		t.Errorf("antigravity granted: hint %q; want it to say Antigravity is read", got)
	}
}

// Without a consumer plan the hint is what it was: Gemini CLI is for Gemini
// Code Assist Standard and Enterprise licences and API keys.
func TestGeminiCLIHintWithoutAConsumerPlan(t *testing.T) {
	cfg := Default()
	if got := cfg.VendorUsageConfigHint("gemini-cli"); !strings.Contains(got, "vendor_usage.gemini_cli.enabled: true") || strings.Contains(got, "Antigravity") {
		t.Errorf("off: hint %q", got)
	}
	cfg.VendorUsage.GeminiCLI.Enabled = true
	if got := cfg.VendorUsageConfigHint("gemini-cli"); got != "" {
		t.Errorf("on: hint %q, want none", got)
	}
}
