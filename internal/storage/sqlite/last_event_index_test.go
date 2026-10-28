package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// TestLastEventBySourceIsCovered pins the source-health lookup to an
// index. It grouped the whole events table by an expression over source,
// which no index could answer: on a store of ~345,000 events and a loaded
// machine it missed the assessment's 5-second deadline, and every source
// was reported as never having produced an event.
func TestLastEventBySourceIsCovered(t *testing.T) {
	s := newTestStore(t)
	plan := explain(t, s, lastEventBySourceQuery, nil)
	if !strings.Contains(plan, "COVERING INDEX events_source_ts_idx") {
		t.Errorf("LastEventBySource reads the table:\n%s", plan)
	}
}

// Events with no source are reported under "(none)", as before the query
// grouped the raw column.
func TestLastEventBySourceReportsUnsourcedEventsAsNone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	env := mustPromptEnvelope(t, "x", at, &eventschema.PromptEvent{
		PromptHash: "h-x", Provider: eventschema.ProviderAnthropic,
		RequestModel: "claude", InputTokens: 1, OutputTokens: 1,
	})
	env.Source = ""
	if err := s.Append(ctx, env); err != nil {
		t.Fatalf("append: %v", err)
	}
	last, err := s.LastEventBySource(ctx)
	if err != nil {
		t.Fatalf("LastEventBySource: %v", err)
	}
	if got := last["(none)"]; !got.Equal(at) {
		t.Errorf("(none) last = %s, want %s; got %v", got, at, last)
	}
	if _, ok := last[""]; ok {
		t.Errorf("an empty source was reported as its own key: %v", last)
	}
}
