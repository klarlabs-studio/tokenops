package coach

import (
	"strings"
	"testing"
)

func TestEffortChange(t *testing.T) {
	tests := []struct {
		name, client, model, level string
		ok                         bool
		want                       []string
	}{
		{name: "claude code saves a level per model", client: "claude-code", model: "claude-opus-5-5", level: "medium", ok: true,
			want: []string{"~/.claude/settings.json", `"modelSettings"`, `"claude-opus-5-5"`, `"effortLevel": "medium"`, "/effort medium"}},
		{name: "claude code has no saved level beyond xhigh", client: "claude-code", model: "claude-opus-5-5", level: "max"},
		{name: "claude code has no saved level below low", client: "claude-code", model: "claude-opus-5-5", level: "minimal"},
		{name: "codex sets one level for every model", client: "codex", model: "gpt-5.5", level: "medium", ok: true,
			want: []string{"~/.codex/config.toml", `model_reasoning_effort = "medium"`, "every model"}},
		{name: "opencode is advice only", client: "opencode", model: "claude-opus-5-5", level: "low"},
		{name: "an unknown client is advice only", client: "cursor", model: "m", level: "low"},
		{name: "a level is not quoted into a setting unchecked", client: "codex", model: "gpt-5.5", level: `low"` + "\n" + `x = "y`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := EffortChange(tt.client, tt.model, tt.level)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (%q)", ok, tt.ok, got)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("change %q does not contain %q", got, w)
				}
			}
		})
	}
}
