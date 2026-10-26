package promptcache_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/promptcache"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type eventsReader struct{ envs []*eventschema.Envelope }

func (r *eventsReader) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for _, e := range r.envs {
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

// ccTurn is a Claude Code turn recorded at at, its request sent latency
// before.
func ccTurn(session string, at time.Time, latency time.Duration, write1h int64) *eventschema.Envelope {
	return &eventschema.Envelope{Source: "claude-code-jsonl", Type: eventschema.EventTypePrompt, Timestamp: at,
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, SessionID: session, RequestModel: "claude-opus-4-7",
			Latency: latency, CachedInputTokens: 9_000, CacheWriteInputTokens: 1_000, CacheWrite1hInputTokens: write1h}}
}

// The countdown follows the Claude Code session that ran last, from when
// its last request went out.
func TestPromptCacheCountdown(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	other := &eventschema.Envelope{Source: "codex-jsonl", Timestamp: now.Add(-10 * time.Second),
		Payload: &eventschema.PromptEvent{SessionID: "codex", CachedInputTokens: 10}}
	cases := []struct {
		name  string
		envs  []*eventschema.Envelope
		state waste.CacheState
		left  int64
		none  bool
	}{
		{name: "warm on the hour cache", envs: []*eventschema.Envelope{ccTurn("a", now.Add(-10*time.Minute), 0, 1_000)}, state: waste.CacheWarm, left: 50 * 60},
		{name: "expiring: the request went out 30s before the turn was recorded",
			envs: []*eventschema.Envelope{ccTurn("a", now.Add(-3*time.Minute-20*time.Second), 30*time.Second, 0)}, state: waste.CacheExpiring, left: 70},
		{name: "the latest session counts, not an older one",
			envs:  []*eventschema.Envelope{ccTurn("old", now.Add(-time.Minute), 0, 1_000), ccTurn("new", now.Add(-30*time.Second), 0, 0), other},
			state: waste.CacheWarm, left: 270},
		{name: "cold within the hour", envs: []*eventschema.Envelope{ccTurn("a", now.Add(-20*time.Minute), 0, 0)}, state: waste.CacheCold},
		{name: "cold for over an hour: no session is current", envs: []*eventschema.Envelope{ccTurn("a", now.Add(-66*time.Minute), 0, 0)}, none: true},
		{name: "no Claude Code turns", envs: []*eventschema.Envelope{other}, none: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := promptcache.Current(context.Background(), &eventsReader{envs: c.envs}, now)
			if c.none {
				if got != nil {
					t.Fatalf("countdown = %+v, want none", got)
				}
				return
			}
			if got == nil || got.State != c.state || got.RemainingSeconds != c.left || got.Client != "claude-code" {
				t.Fatalf("countdown = %+v, want %s with %ds left", got, c.state, c.left)
			}
		})
	}
	if promptcache.Current(context.Background(), nil, now) != nil {
		t.Error("a countdown without storage")
	}
}
