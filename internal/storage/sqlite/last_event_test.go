package sqlite

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The staleness check could only ask "were there events in the last 48h?", so
// its warning read the same on day 2 and day 27 of an outage. Answering "when
// did this source last produce anything?" is what lets the warning state the
// size of the gap.
func TestLastEventBySourceReportsTheMostRecentPerSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 7, 11, 11, 36, 0, 0, time.UTC)
	for _, e := range []struct {
		id     string
		source string
		at     time.Time
	}{
		{"a", "claude-code-jsonl", base},
		{"b", "claude-code-jsonl", base.Add(-24 * time.Hour)},
		{"c", "mcp-session", base.Add(48 * time.Hour)},
	} {
		env := mustPromptEnvelope(t, e.id, e.at, &eventschema.PromptEvent{
			PromptHash: "h-" + e.id, Provider: eventschema.ProviderAnthropic,
			RequestModel: "claude", InputTokens: 10, OutputTokens: 5,
		})
		env.Source = e.source
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append %s: %v", e.id, err)
		}
	}

	last, err := s.LastEventBySource(ctx)
	if err != nil {
		t.Fatalf("LastEventBySource: %v", err)
	}

	if got := last["claude-code-jsonl"]; !got.Equal(base) {
		t.Errorf("claude-code-jsonl last = %s, want the most recent %s", got, base)
	}
	if got := last["mcp-session"]; !got.Equal(base.Add(48 * time.Hour)) {
		t.Errorf("mcp-session last = %s", got)
	}
}

// A source that has never produced an event must be absent rather than zero,
// so the caller can tell "never ingested" from "ingested at the epoch".
func TestLastEventBySourceOmitsSourcesWithNoEvents(t *testing.T) {
	s := newTestStore(t)

	last, err := s.LastEventBySource(context.Background())
	if err != nil {
		t.Fatalf("LastEventBySource: %v", err)
	}
	if len(last) != 0 {
		t.Errorf("empty store returned %v", last)
	}
}
