package cli

import (
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// configHintAnthropic reports the missing-piece message operators
// need to see — empty when fully configured, specific otherwise.
func TestConfigHintAnthropic(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.AnthropicUsageConfig
		want string
	}{
		{"disabled", config.AnthropicUsageConfig{}, "set vendor_usage.anthropic.enabled: true + an sk-ant-admin-* key"},
		{"enabled but keyless", config.AnthropicUsageConfig{Enabled: true}, "vendor_usage.anthropic.admin_key is empty; mint a key in the Claude Console"},
		{"fully configured", config.AnthropicUsageConfig{Enabled: true, AdminKey: "sk-ant-admin-x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := configHintAnthropic(c.cfg); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// configHintClaudeCode is symmetric — empty when on, hint when off.
func TestConfigHintClaudeCode(t *testing.T) {
	if got := configHintClaudeCode(true); got != "" {
		t.Errorf("enabled hint should be empty; got %q", got)
	}
	if got := configHintClaudeCode(false); got == "" {
		t.Errorf("disabled hint should be set")
	}
}
