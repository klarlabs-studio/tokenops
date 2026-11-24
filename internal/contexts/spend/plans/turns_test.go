package plans

import (
	"reflect"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestTurnsFrom(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	since := now.Add(-7 * 24 * time.Hour)
	turn := func(provider eventschema.Provider, source string, at time.Time, attrs map[string]string) *eventschema.Envelope {
		return &eventschema.Envelope{Type: eventschema.EventTypePrompt, Source: source, Timestamp: at, Attributes: attrs,
			Payload: &eventschema.PromptEvent{Provider: provider, InputTokens: 10}}
	}
	envs := []*eventschema.Envelope{
		turn(eventschema.ProviderCursor, "cursor-hook", now.Add(-time.Hour), nil),
		turn(eventschema.ProviderCursor, "cursor-hook", now.Add(-3*time.Hour), nil),
		turn(eventschema.ProviderCursor, "cursor-hook", now.Add(-8*24*time.Hour), nil),                                                           // before the window
		turn(eventschema.ProviderAnthropic, "claude-code-statusline", now.Add(-time.Minute), map[string]string{"granularity": "quota_snapshot"}), // a window reading
		turn(eventschema.ProviderOpenAI, "codex-jsonl", now.Add(-2*time.Hour), map[string]string{"granularity": "day"}),                          // a day's totals
		turn(eventschema.ProviderOpenAI, "codex-jsonl", now.Add(-4*time.Hour), map[string]string{"granularity": "assistant_turn"}),
		nil,
	}
	want := map[string]ProviderTurns{
		"cursor": {Turns: 2, LastAt: now.Add(-time.Hour)},
		"openai": {Turns: 1, LastAt: now.Add(-4 * time.Hour)},
	}
	if got := TurnsFrom(envs, since, now); !reflect.DeepEqual(got, want) {
		t.Errorf("TurnsFrom = %v, want %v", got, want)
	}
}
