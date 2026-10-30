package attribution

import (
	"os"
	"path/filepath"
	"testing"
)

// A hook inside a Claude Code session names who bills its turns now, so a
// budget on one provider's bill binds only that provider's sessions.
func TestClaudeCodeSessionBiller(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // no settings: the base URL comes from env
	dir := t.TempDir()
	transcript := filepath.Join(dir, "s.jsonl")
	line := `{"type":"assistant","timestamp":"2026-10-30T09:00:00.000Z","sessionId":"s","message":{"id":"m1","model":"accounts/fireworks/models/kimi-k3","usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		env        map[string]string
		transcript string
		model      string
		want       string
	}{
		"no base URL is Anthropic":         {nil, "", "", "anthropic"},
		"Anthropic's own host":             {map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com"}, "", "", "anthropic"},
		"a vendor that bills every turn":   {map[string]string{"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}, "", "claude-opus-4-7", "deepseek"},
		"Fireworks serving its own model":  {map[string]string{"ANTHROPIC_BASE_URL": "https://api.fireworks.ai/inference"}, "", "accounts/fireworks/models/kimi-k3", "fireworks"},
		"Fireworks running Claude":         {map[string]string{"ANTHROPIC_BASE_URL": "https://api.fireworks.ai/inference"}, "", "claude-opus-4-7", "anthropic"},
		"the model from the transcript":    {map[string]string{"ANTHROPIC_BASE_URL": "https://api.fireworks.ai/inference"}, transcript, "", "fireworks"},
		"a local relay serving Claude":     {map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:7878"}, "", "claude-opus-4-7", "anthropic"},
		"a local relay, unknown model":     {map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:7878"}, "", "mystery-model", ""},
		"Bedrock is billed by AWS":         {map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, "", "claude-opus-4-7", ""},
		"a cloud switch set to 0 is unset": {map[string]string{"CLAUDE_CODE_USE_VERTEX": "0"}, "", "", "anthropic"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			if got := ClaudeCodeSessionBiller(getenv, c.transcript, c.model); got != c.want {
				t.Errorf("biller = %q, want %q", got, c.want)
			}
		})
	}
}
