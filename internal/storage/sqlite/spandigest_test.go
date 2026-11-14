package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SpanDigest moves with every change the change log misses: inserts,
// deletes and each update the store makes to events older than its
// horizon. A read, a change outside the span and an attribute-only change
// leave it.
func TestSpanDigestMovesWithEveryChangeInTheSpan(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	old := cacheNow.Add(-80 * 24 * time.Hour)
	since, until := old.Add(-5*24*time.Hour), old.Add(5*24*time.Hour)
	envs := make([]*eventschema.Envelope, 0, 40)
	for i := range 40 {
		envs = append(envs, promptAt(fmt.Sprintf("e%02d", i), old.Add(time.Duration(i)*time.Hour), eventschema.ProviderAnthropic, int64(100+i)))
	}
	envs = append(envs, promptAt("recent", cacheNow.Add(-time.Hour), eventschema.ProviderAnthropic, 5))
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	digest := func() string {
		t.Helper()
		d, err := s.SpanDigest(ctx, eventschema.EventTypePrompt, since, until)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	steps := []struct {
		name  string
		do    func()
		moves bool
	}{
		{"a read", func() {
			if _, err := s.SessionUsage(ctx, analytics.Filter{Since: since}); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a change outside the span", func() { execAll(t, s, `UPDATE events SET input_tokens = 7 WHERE id = 'recent'`) }, false},
		{"an attribute changed", func() { execAll(t, s, `UPDATE events SET attributes = '{"session":"x"}' WHERE id = 'e01'`) }, false},
		{"an insert", func() {
			if err := s.Append(ctx, promptAt("late", old.Add(time.Minute), eventschema.ProviderOpenAI, 9)); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a delete", func() { execAll(t, s, `DELETE FROM events WHERE id = 'e02'`) }, true},
		{"re-attributed", func() {
			if n, err := s.Reattribute(ctx, "claude-code-jsonl", "claude-opus-5", "anthropic", "openrouter", "openrouter.ai", since, until, true); err != nil || n == 0 {
				t.Fatalf("re-attributed %d: %v", n, err)
			}
		}, true},
		{"a model renamed, same length", func() { execAll(t, s, `UPDATE events SET model = 'claude-opus-6' WHERE id = 'e03'`) }, true},
		{"restamped", func() {
			if n, err := s.RestampPlanIncluded(ctx, "openai", since, until, nil); err != nil || n == 0 {
				t.Fatalf("restamped %d: %v", n, err)
			}
		}, true},
		{"tokens corrected", func() { execAll(t, s, `UPDATE events SET output_tokens = output_tokens - 1 WHERE id = 'e04'`) }, true},
		{"a cost stored", func() { execAll(t, s, `UPDATE events SET cost_usd = 0.5 WHERE id = 'e05'`) }, true},
		{"a source changed", func() { execAll(t, s, `UPDATE events SET source = 'codex-jsonl' WHERE id = 'e06'`) }, true},
		{"moved in time", func() { execAll(t, s, `UPDATE events SET timestamp_ns = timestamp_ns + 1 WHERE id = 'e07'`) }, true},
		{"moved out of the span", func() {
			execAll(t, s, fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'e08'`, cacheNow.UnixNano()))
		}, true},
	}
	was := digest()
	for _, st := range steps {
		st.do()
		is := digest()
		if moved := is != was; moved != st.moves {
			t.Errorf("%s: digest moved = %v, want %v", st.name, moved, st.moves)
		}
		was = is
	}
}

// The digest reads the usage index alone, never the table.
func TestSpanDigestIsCovered(t *testing.T) {
	s := newTestStore(t)
	plan := explain(t, s, spanDigestSQL, []any{string(eventschema.EventTypePrompt), int64(0), int64(1)})
	if !strings.Contains(plan, "COVERING INDEX events_usage_idx") {
		t.Fatalf("the span digest reads the table:\n%s", plan)
	}
}
