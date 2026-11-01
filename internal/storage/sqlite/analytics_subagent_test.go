package sqlite

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Spend groups by session and by who in it made the turn: the session
// itself (empty key), or a subagent by its type, "subagent" when its type
// is not known.
func TestBucketUsageGroupsBySessionAndSubagent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	add := func(id, session string, tokens int64, attrs map[string]string) {
		t.Helper()
		env := &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: "claude-code-jsonl", Attributes: attrs,
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-7",
				InputTokens: tokens, TotalTokens: tokens, SessionID: session},
		}
		if err := s.Append(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	add("e1", "s1", 100, nil)
	add("e2", "s1", 30, map[string]string{"subagent_id": "a1", "subagent_type": "Explore"})
	add("e3", "s2", 7, map[string]string{"subagent_id": "a2", "subagent_type": ""})
	add("e4", "s2", 1, map[string]string{"subagent_id": "a3", "subagent_type": "Explore"})

	tokens := func(g analytics.Group) map[string]int64 {
		t.Helper()
		u, err := s.BucketUsage(ctx, analytics.Filter{}, 86400, g)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for _, r := range u.Rows {
			out[r.GroupKey] += r.TotalTokens
		}
		return out
	}
	if got := tokens(analytics.GroupSession); got["s1"] != 130 || got["s2"] != 8 {
		t.Errorf("by session = %v", got)
	}
	if got := tokens(analytics.GroupSubagent); got[""] != 100 || got["Explore"] != 31 || got["subagent"] != 7 {
		t.Errorf("by subagent = %v", got)
	}
}
