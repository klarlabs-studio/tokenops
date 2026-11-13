package sqlite

import (
	"context"
	"database/sql"
	"fmt"
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

// LastEventBySource answers from one index seek per source, not a walk of
// every event: a store of hundreds of thousands of events was read in full
// every thirty seconds for the source freshness check.
func TestLastEventBySourceSeeksPerSource(t *testing.T) {
	s := newTestStore(t)
	plan := explain(t, s, lastEventBySourceQuery, nil)
	if strings.Contains(plan, "SCAN") && !strings.Contains(plan, "SEARCH") {
		t.Errorf("LastEventBySource walks the whole index:\n%s", plan)
	}
}

// The per-source query returns the same answer as the grouped one, for
// every source the store holds, unsourced events included.
func TestLastEventBySourceMatchesTheGroupedQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	for i, src := range []string{"claude-code-jsonl", "", "codex-jsonl", "claude-code-jsonl", "mcp-session", "codex-jsonl", "zed"} {
		env := mustPromptEnvelope(t, fmt.Sprintf("e%d", i), base.Add(time.Duration(i*7)*time.Minute), &eventschema.PromptEvent{
			PromptHash: fmt.Sprintf("h-%d", i), Provider: eventschema.ProviderAnthropic,
			RequestModel: "claude", InputTokens: 1, OutputTokens: 1,
		})
		env.Source = src
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	got, err := s.LastEventBySource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Time{}
	rows, err := s.db.QueryContext(ctx, `SELECT source, MAX(timestamp_ns) FROM events GROUP BY source`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var src sql.NullString
		var ns sql.NullInt64
		if err := rows.Scan(&src, &ns); err != nil {
			t.Fatal(err)
		}
		key := src.String
		if key == "" {
			key = "(none)"
		}
		if at := time.Unix(0, ns.Int64).UTC(); at.After(want[key]) {
			want[key] = at
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if !got[k].Equal(v) {
			t.Errorf("%s: got %s, want %s", k, got[k], v)
		}
	}
}
