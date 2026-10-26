package headroom_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ccTurn is a Claude Code turn recorded at at, its request sent latency
// before.
func ccTurn(session string, at time.Time, latency time.Duration, write1h int64) *eventschema.Envelope {
	return &eventschema.Envelope{Source: "claude-code-jsonl", Type: eventschema.EventTypePrompt, Timestamp: at,
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, SessionID: session, RequestModel: "claude-opus-4-7",
			Latency: latency, CachedInputTokens: 9_000, CacheWriteInputTokens: 1_000, CacheWrite1hInputTokens: write1h}}
}

// The glance carries it, and the session's ID does not leave with it.
func TestGlanceCarriesThePromptCache(t *testing.T) {
	now := time.Now().UTC()
	g, err := headroom.ComputeGlance(context.Background(), headroom.Deps{Reader: &eventsReader{envs: []*eventschema.Envelope{ccTurn("secret-session", now.Add(-time.Minute), 0, 0)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(g.Payload())
	if !strings.Contains(string(b), `"prompt_cache":{"client":"claude-code"`) || strings.Contains(string(b), "secret-session") {
		t.Errorf("payload = %s", b)
	}
}
